package library

import "time"

// Evidence points to an observed excerpt, never an invented bibliographic source.
type Evidence struct {
	ExcerptKey string `json:"excerpt_key,omitempty"` // Worker resolves a source-bound catalog entry before publication.
	MaterialID int64  `json:"material_id"`
	Source     int    `json:"source"`
	Locator    string `json:"locator"`
	Excerpt    string `json:"excerpt"`
}

type Direction struct {
	Question      string   `json:"question"`
	NearestWork   string   `json:"nearest_work"`
	Change        string   `json:"change"`
	Baseline      string   `json:"baseline"`
	Dataset       string   `json:"dataset"`
	Metrics       []string `json:"metrics"`
	Steps         []string `json:"steps"`
	Risks         []string `json:"risks"`
	Falsification string   `json:"falsification"`
	Verification  string   `json:"verification"`
}

type MapNode struct {
	Key         string         `json:"key"`
	Parent      string         `json:"parent"`
	Kind        string         `json:"kind"` // topic / problem / method / limitation / direction / paper
	Title       string         `json:"title"`
	Text        string         `json:"text"`
	Basis       string         `json:"basis"` // author_report / system_synthesis / hypothesis / index
	MaterialIDs []int64        `json:"material_ids"`
	Evidence    []Evidence     `json:"evidence"`
	Direction   *Direction     `json:"direction,omitempty"`
	Annotation  *MapAnnotation `json:"annotation,omitempty"`
}

type MapLink struct {
	Key         string  `json:"key"`
	From        string  `json:"from"`
	TargetTopic string  `json:"target_topic"`
	Kind        string  `json:"kind"` // combination / transfer / disagreement / constraint / shared_paper
	Reason      string  `json:"reason"`
	MaterialIDs []int64 `json:"material_ids"`
}

type MapAnnotation struct {
	Title   string    `json:"title"`
	Notes   string    `json:"notes"`
	Idea    string    `json:"idea"`
	Pinned  bool      `json:"pinned"`
	Updated time.Time `json:"updated"`
}

type KnowledgeBranch struct {
	Annotations    map[string]MapAnnotation `json:"annotations,omitempty"`
	Topic          string                   `json:"topic"`
	Domain         string                   `json:"domain"`
	Group          string                   `json:"group"`
	Version        int64                    `json:"version"`
	CorpusRevision int64                    `json:"corpus_revision"`
	ReviewVersion  int64                    `json:"review_version"`
	Model          string                   `json:"model"`
	Nodes          []MapNode                `json:"nodes"`
	Links          []MapLink                `json:"links"`
	Coverage       []int64                  `json:"coverage"`
	References     []Material               `json:"references"`
	Changes        []string                 `json:"changes"`
	Created        time.Time                `json:"created"`
}

type MapBranchStatus struct {
	Topic     Topic           `json:"topic"`
	Branch    KnowledgeBranch `json:"branch"`
	Dirty     bool            `json:"dirty"`
	LastError string          `json:"last_error,omitempty"`
}

type KnowledgeMap struct {
	Title      string            `json:"title"`
	Disclosure string            `json:"disclosure"`
	Branches   []MapBranchStatus `json:"branches"`
}

type MapSnapshot struct {
	Snapshot Snapshot        `json:"snapshot"`
	Previous KnowledgeBranch `json:"previous"`
	Topics   []Topic         `json:"topics"`
}
