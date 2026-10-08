// Package scholar discovers sources through real network requests, keeping
// discovery records separate from verified full-text evidence.
package scholar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
)

type Keys struct {
	OpenAlex  string `json:"openalex"`
	AnySearch string `json:"anysearch"`
}
type Candidate struct {
	Source    library.Source `json:"source"`
	Abstract  string         `json:"abstract"`
	FullURL   string         `json:"full_url"`
	Providers []string       `json:"providers"`
}
type Discovery struct {
	Query      string      `json:"query"`
	Candidates []Candidate `json:"candidates"`
	Errors     []string    `json:"errors"`
	Checked    time.Time   `json:"checked"`
}
type Client struct {
	HTTP                      *http.Client
	Keys                      Keys
	OpenAlexURL, AnySearchURL string
}

func New(proxy string, keys Keys) (*Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = publicDial("")
	if proxy != "" {
		u, e := url.Parse(proxy)
		if e != nil || u.Scheme != "socks5" || u.User != nil || u.Port() == "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			return nil, errors.New("invalid_scholar_proxy")
		}
		tr.DialContext = publicDial(u.Host)
	}
	c := &Client{Keys: keys, OpenAlexURL: "https://api.openalex.org/works", AnySearchURL: "https://api.anysearch.com/mcp"}
	c.HTTP = &http.Client{Transport: tr, Timeout: 35 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("redirect_limit")
		}
		return SafeURL(r.URL.String())
	}}
	return c, nil
}
func SafeURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return errors.New("invalid_source_url")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errors.New("private_source_url")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return errors.New("private_source_url")
	}
	return nil
}
func (c *Client) get(ctx context.Context, raw string) ([]byte, string, error) {
	if e := SafeURL(raw); e != nil {
		return nil, "", e
	}
	r, e := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if e != nil {
		return nil, "", e
	}
	r.Header.Set("User-Agent", "wechat-go-assistant/1.0 (personal literature library)")
	res, e := c.HTTP.Do(r)
	if e != nil {
		return nil, "", errors.New("source_network_error")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, "", fmt.Errorf("source_http_%d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, (24<<20)+1))
	if len(b) > 24<<20 {
		return nil, "", errors.New("source_requires_manual_large_download")
	}
	return b, res.Header.Get("Content-Type"), e
}
func (c *Client) Discover(ctx context.Context, query string) Discovery {
	d := Discovery{Query: query, Checked: time.Now().UTC()}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, provider := range []string{"openalex", "anysearch"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			var ms []Candidate
			var e error
			if p == "openalex" {
				ms, e = c.openalex(ctx, query)
			} else {
				ms, e = c.anysearch(ctx, query)
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				d.Errors = append(d.Errors, p+":"+e.Error())
			} else {
				d.Candidates = append(d.Candidates, ms...)
			}
		}(provider)
	}
	wg.Wait()
	by := map[string]int{}
	merged := []Candidate{}
	for _, v := range d.Candidates {
		k := strings.ToLower(v.Source.DOI)
		if k == "" {
			k = strings.ToLower(v.Source.Title) + "|" + strings.Join(v.Source.Authors, "|")
		}
		if i, ok := by[k]; ok {
			merged[i].Providers = append(merged[i].Providers, v.Providers...)
			if merged[i].FullURL == "" {
				merged[i].FullURL = v.FullURL
			}
		} else {
			by[k] = len(merged)
			merged = append(merged, v)
		}
	}
	d.Candidates = merged
	sort.Strings(d.Errors)
	return d
}
func (c *Client) openalex(ctx context.Context, query string) ([]Candidate, error) {
	u, _ := url.Parse(c.OpenAlexURL)
	q := u.Query()
	q.Set("search", query)
	q.Set("per-page", "20")
	if c.Keys.OpenAlex != "" {
		q.Set("api_key", c.Keys.OpenAlex)
	}
	u.RawQuery = q.Encode()
	b, _, e := c.get(ctx, u.String())
	if e != nil {
		return nil, e
	}
	var response struct {
		Results []struct {
			Title   string `json:"title"`
			DOI     string `json:"doi"`
			Year    int    `json:"publication_year"`
			Authors []struct {
				Author struct {
					Name string `json:"display_name"`
				} `json:"author"`
			} `json:"authorships"`
			Abstract map[string][]int `json:"abstract_inverted_index"`
			Location struct {
				PDF     string `json:"pdf_url"`
				URL     string `json:"landing_page_url"`
				Version string `json:"version"`
			} `json:"best_oa_location"`
		} `json:"results"`
	}
	if e = json.Unmarshal(b, &response); e != nil {
		return nil, e
	}
	out := []Candidate{}
	for _, w := range response.Results {
		v := Candidate{Source: library.Source{Title: w.Title, DOI: strings.TrimPrefix(w.DOI, "https://doi.org/"), Year: w.Year, URL: w.DOI, Version: w.Location.Version, ReadingScope: "metadata_only", Provider: "openalex"}, FullURL: w.Location.PDF, Providers: []string{"openalex"}}
		for _, a := range w.Authors {
			v.Source.Authors = append(v.Source.Authors, a.Author.Name)
		}
		if v.FullURL == "" {
			v.FullURL = w.Location.URL
		}
		if v.Source.URL == "" {
			v.Source.URL = w.Location.URL
		}
		positions := map[int]string{}
		last := 0
		for word, ps := range w.Abstract {
			for _, p := range ps {
				positions[p] = word
				if p > last {
					last = p
				}
			}
		}
		if len(positions) > 0 {
			parts := []string{}
			for p := 0; p <= last; p++ {
				parts = append(parts, positions[p])
			}
			v.Abstract = strings.Join(parts, " ")
			v.Source.ReadingScope = "abstract_only"
		}
		out = append(out, v)
	}
	return out, nil
}
func (c *Client) anysearch(ctx context.Context, query string) ([]Candidate, error) {
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "search", "arguments": map[string]any{"query": query, "domain": "academic", "sub_domain": "academic.search", "content_types": []string{"academic"}, "max_results": 20}}}
	b, _ := json.Marshal(payload)
	r, _ := http.NewRequestWithContext(ctx, "POST", c.AnySearchURL, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if c.Keys.AnySearch != "" {
		r.Header.Set("Authorization", "Bearer "+c.Keys.AnySearch)
	}
	res, e := c.HTTP.Do(r)
	if e != nil {
		return nil, errors.New("search_network_error")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("search_http_%d", res.StatusCode)
	}
	var v struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&v); e != nil {
		return nil, e
	}
	if len(v.Error) > 0 {
		return nil, errors.New("search_rpc_error")
	}
	out := []Candidate{}
	heading := regexp.MustCompile(`(?m)^###\s+\d+[.)]\s+(.+)$`)
	urls := regexp.MustCompile(`https://[^\s<>\)]+`)
	for _, item := range v.Result.Content {
		var entries []struct {
			Title, URL, DOI, Abstract string
			Authors                   []string
			Year                      int
		}
		if json.Unmarshal([]byte(item.Text), &entries) == nil {
			for _, x := range entries {
				out = append(out, Candidate{Source: library.Source{Title: x.Title, URL: x.URL, DOI: x.DOI, Authors: x.Authors, Year: x.Year, Provider: "anysearch", ReadingScope: "metadata_only"}, Abstract: x.Abstract, Providers: []string{"anysearch"}})
			}
			continue
		}
		matches := heading.FindAllStringSubmatchIndex(item.Text, -1)
		for i, m := range matches {
			end := len(item.Text)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			block := item.Text[m[1]:end]
			links := urls.FindAllString(block, -1)
			if len(links) == 0 {
				continue
			}
			src := library.Source{Title: item.Text[m[2]:m[3]], URL: links[0], Provider: "anysearch", ReadingScope: "metadata_only"}
			for _, u := range links {
				if strings.HasPrefix(u, "https://doi.org/") {
					src.DOI = strings.TrimPrefix(u, "https://doi.org/")
					src.URL = u
				}
			}
			out = append(out, Candidate{Source: src, Providers: []string{"anysearch"}})
		}
	}
	return out, nil
}
func (c *Client) Fetch(ctx context.Context, raw string) ([]byte, string, error) {
	return c.get(ctx, raw)
}
