// Package library stores durable source material and versioned topic reviews.
package library

import "time"

type Asset struct {
	SHA256 string `json:"sha256"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
}
type Intake struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	Conversation string    `json:"conversation"`
	Collection   string    `json:"collection"`
	Text         string    `json:"text"`
	Assets       []Asset   `json:"assets"`
	State        string    `json:"state"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}
type Source struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	DOI           string   `json:"doi,omitempty"`
	Authors       []string `json:"authors,omitempty"`
	Year          int      `json:"year,omitempty"`
	Version       string   `json:"version,omitempty"`
	ReadingScope  string   `json:"reading_scope"`
	Verified      bool     `json:"verified"`
	Provider      string   `json:"provider,omitempty"`
	ContentSHA256 string   `json:"content_sha256,omitempty"`
}
type Claim struct {
	Field       string `json:"field"`
	Text        string `json:"text"`
	Source      int    `json:"source"`
	Locator     string `json:"locator"`
	Excerpt     string `json:"excerpt"`
	Attribution string `json:"attribution"`
}
type Material struct {
	ID         int64     `json:"id"`
	Owner      string    `json:"owner,omitempty"`
	IntakeID   string    `json:"intake_id,omitempty"`
	Title      string    `json:"title"`
	Collection string    `json:"collection"`
	Topics     []string  `json:"topics"`
	Tags       []string  `json:"tags"`
	Text       string    `json:"text"`
	Summary    string    `json:"summary"`
	Sources    []Source  `json:"sources"`
	Claims     []Claim   `json:"claims"`
	Assets     []Asset   `json:"assets"`
	Missing    []string  `json:"missing"`
	Revision   int64     `json:"revision"`
	Deleted    bool      `json:"deleted"`
	Updated    time.Time `json:"updated"`
}
type Topic struct {
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	Version   int64  `json:"version"`
	Dirty     bool   `json:"dirty"`
	LastError string `json:"last_error,omitempty"`
	Notes     string `json:"notes"`
	Count     int    `json:"count"`
}
type Section struct {
	Name        string  `json:"name"`
	Text        string  `json:"text"`
	MaterialIDs []int64 `json:"material_ids"`
}
type Review struct {
	Topic          string     `json:"topic"`
	Version        int64      `json:"version"`
	CorpusRevision int64      `json:"corpus_revision"`
	Notes          string     `json:"notes"`
	Sections       []Section  `json:"sections"`
	Changes        []string   `json:"changes"`
	Created        time.Time  `json:"created"`
	References     []Material `json:"references,omitempty"`
}
type Snapshot struct {
	Topic     Topic      `json:"topic"`
	Materials []Material `json:"materials"`
	Previous  Review     `json:"previous"`
}
type Research struct {
	Materials []Material `json:"materials"`
	Problems  []string   `json:"problems"`
}
type Request struct {
	Owner        string    `json:"owner"`
	Action       string    `json:"action"`
	Key          string    `json:"key,omitempty"`
	ID           string    `json:"id,omitempty"`
	MaterialID   int64     `json:"material_id,omitempty"`
	Version      int64     `json:"version,omitempty"`
	Conversation string    `json:"conversation,omitempty"`
	Collection   string    `json:"collection,omitempty"`
	Text         string    `json:"text,omitempty"`
	Topics       []string  `json:"topics,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	Query        string    `json:"query,omitempty"`
	State        string    `json:"state,omitempty"`
	Review       *Review   `json:"review,omitempty"`
	Research     *Research `json:"research,omitempty"`
}
