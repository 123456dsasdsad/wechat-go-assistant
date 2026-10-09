package weixin

import (
	"encoding/xml"
	"errors"
	"html"
	"io"
	"strconv"
	"strings"
)

// Source is normalized by adapters, never treated as a user instruction.
type Source struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Speaker string   `json:"speaker,omitempty"`
	Time    string   `json:"time,omitempty"`
	Kind    string   `json:"kind"`
	Missing []string `json:"missing,omitempty"`
}
type AppContent struct {
	Text    string
	Ref     *RefMessage
	Sources []Source
}
type app struct {
	Type   int    `xml:"type"`
	Title  string `xml:"title"`
	Des    string `xml:"des"`
	URL    string `xml:"url"`
	Record string `xml:"recorditem"`
	Ref    struct {
		ID      string `xml:"svrid"`
		Content string `xml:"content"`
		Name    string `xml:"displayname"`
	} `xml:"refermsg"`
}
type record struct {
	Items []struct {
		ID      string `xml:"dataid,attr"`
		Type    int    `xml:"datatype,attr"`
		Text    string `xml:"datadesc"`
		Title   string `xml:"datatitle"`
		Speaker string `xml:"sourcename"`
		Time    string `xml:"sourcetime"`
		Record  string `xml:"recorditem"`
	} `xml:"datalist>dataitem"`
}

func boundedXML(raw string) error {
	if len(raw) > 2<<20 {
		return errors.New("forward_too_large")
	}
	d := xml.NewDecoder(strings.NewReader(raw))
	depth := 0
	for {
		t, e := d.Token()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return errors.New("invalid_forward_xml")
		}
		switch t.(type) {
		case xml.StartElement:
			depth++
			if depth > 64 {
				return errors.New("forward_xml_too_deep")
			}
		case xml.EndElement:
			depth--
		}
	}
}
func ParseAppMessage(raw string) (AppContent, error) {
	var out AppContent
	if !strings.HasPrefix(strings.TrimSpace(raw), "<") {
		raw = html.UnescapeString(raw)
	}
	if e := boundedXML(raw); e != nil {
		return out, e
	}
	var envelope struct {
		App app `xml:"appmsg"`
	}
	if e := xml.Unmarshal([]byte(raw), &envelope); e != nil {
		return out, e
	}
	a := envelope.App
	if strings.HasPrefix(strings.TrimSpace(raw), "<appmsg") {
		if e := xml.Unmarshal([]byte(raw), &a); e != nil {
			return out, e
		}
	}
	switch a.Type {
	case 19:
		e := parseRecord(a.Record, 0, "", &out.Sources)
		return out, e
	case 5:
		out.Sources = []Source{{ID: "article", Kind: "article", Text: strings.Join([]string{a.Title, a.Des, a.URL}, "\n")}}
	case 57:
		out.Text = a.Title
		out.Ref = &RefMessage{ServerID: ID(a.Ref.ID), Title: a.Ref.Name, Item: &Item{Type: TextType, Text: &TextItem{Text: a.Ref.Content}}}
	default:
		return out, errors.New("unsupported_app_message")
	}
	return out, nil
}
func parseRecord(raw string, depth int, prefix string, out *[]Source) error {
	if depth > 6 {
		return errors.New("forward_nested_too_deep")
	}
	if raw == "" {
		return errors.New("empty_forward_record")
	}
	if !strings.HasPrefix(strings.TrimSpace(raw), "<") {
		raw = html.UnescapeString(raw)
	}
	if e := boundedXML(raw); e != nil {
		return e
	}
	var r record
	if e := xml.Unmarshal([]byte(raw), &r); e != nil {
		return e
	}
	if len(r.Items) == 0 {
		return errors.New("empty_forward_record")
	}
	for i, v := range r.Items {
		id := prefix + strconv.Itoa(i+1)
		if v.Record != "" {
			if e := parseRecord(v.Record, depth+1, id+".", out); e != nil {
				return e
			}
			continue
		}
		s := Source{ID: id, Text: strings.TrimSpace(v.Title + "\n" + v.Text), Speaker: v.Speaker, Time: v.Time, Kind: "text"}
		if v.Type != 1 {
			s.Kind = "attachment"
			s.Missing = []string{"原转发附件未包含可用下载凭证（类型" + strconv.Itoa(v.Type) + "），请单独提供原件"}
		}
		*out = append(*out, s)
		if len(*out) > 1000 {
			return errors.New("too_many_forward_entries")
		}
	}
	return nil
}
