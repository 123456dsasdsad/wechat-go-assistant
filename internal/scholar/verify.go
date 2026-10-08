package scholar

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
)

func normalizeTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// Verify checks identity against a DOI registrar, independently of search results.
func (c *Client) Verify(ctx context.Context, src library.Source) (library.Source, error) {
	doi := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(src.DOI), "https://doi.org/"), "http://doi.org/")
	if doi == "" {
		return src, errors.New("no_primary_identifier")
	}
	body, _, e := c.get(ctx, "https://api.crossref.org/works/"+url.PathEscape(doi))
	if e != nil {
		return src, e
	}
	var v struct {
		Message struct {
			DOI       string                           `json:"DOI"`
			Title     []string                         `json:"title"`
			Author    []struct{ Given, Family string } `json:"author"`
			Published struct {
				DateParts [][]int `json:"date-parts"`
			} `json:"published"`
		} `json:"message"`
	}
	if json.Unmarshal(body, &v) != nil || !strings.EqualFold(doi, v.Message.DOI) || len(v.Message.Title) == 0 || normalizeTitle(src.Title) != normalizeTitle(v.Message.Title[0]) {
		return src, errors.New("primary_identity_mismatch")
	}
	src.DOI = v.Message.DOI
	src.Verified = true
	src.Authors = nil
	for _, a := range v.Message.Author {
		src.Authors = append(src.Authors, strings.TrimSpace(a.Given+" "+a.Family))
	}
	if len(v.Message.Published.DateParts) > 0 && len(v.Message.Published.DateParts[0]) > 0 {
		src.Year = v.Message.Published.DateParts[0][0]
	}
	return src, nil
}
