package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/scholar"
	"strings"
	"unicode"
)

func canonicalSource(raw, text string, candidates []scholar.Candidate) library.Source {
	normalize := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, s)
	}
	prefix := []rune(text)
	if len(prefix) > 5000 {
		prefix = prefix[:5000]
	}
	head := normalize(string(prefix))
	for _, c := range candidates {
		title := normalize(c.Source.Title)
		if len(title) < 12 || !strings.Contains(head, title) {
			continue
		}
		if c.Source.Provider != "openalex" {
			continue
		}
		src := c.Source
		src.URL = raw
		src.Verified = true
		src.ReadingScope = "partial_text"
		return src
	}
	return library.Source{URL: raw, Title: raw, Verified: true, ReadingScope: "partial_text", Provider: "linked_original"}
}
