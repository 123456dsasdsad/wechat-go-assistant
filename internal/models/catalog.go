package models

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
)

type Choice struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}
type Model struct {
	ID            string   `json:"id"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"default_effort"`
}
type Catalog struct {
	Models []Model `json:"models"`
}

var modelID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

func ValidID(id string) bool { return modelID.MatchString(id) }
func ValidEffort(e string) bool {
	switch e {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return true
	}
	return false
}
func Load(path string) (Catalog, error) {
	var c Catalog
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 64*1024 || json.Unmarshal(b, &c) != nil {
		return c, errors.New("model_catalog_unreadable")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
func (c Catalog) Validate() error {
	if len(c.Models) == 0 || len(c.Models) > 32 {
		return errors.New("invalid_model_catalog")
	}
	seen := map[string]bool{}
	for _, m := range c.Models {
		if !ValidID(m.ID) || seen[m.ID] || len(m.Efforts) == 0 || len(m.Efforts) > 8 {
			return errors.New("invalid_model_catalog")
		}
		seen[m.ID] = true
		defaultFound := false
		seenEfforts := map[string]bool{}
		for _, effort := range m.Efforts {
			if !ValidEffort(effort) || seenEfforts[effort] {
				return errors.New("invalid_model_catalog")
			}
			seenEfforts[effort] = true
			defaultFound = defaultFound || effort == m.DefaultEffort
		}
		if !defaultFound {
			return errors.New("invalid_model_default_effort")
		}
	}
	return nil
}
func (c Catalog) Resolve(id, effort string) (Choice, error) {
	if n, err := strconv.Atoi(id); err == nil && n >= 1 && n <= len(c.Models) {
		id = c.Models[n-1].ID
	}
	for _, m := range c.Models {
		if m.ID != id {
			continue
		}
		if effort == "" {
			effort = m.DefaultEffort
		}
		for _, supported := range m.Efforts {
			if effort == supported {
				return Choice{id, effort}, nil
			}
		}
		return Choice{}, errors.New("unsupported_reasoning_effort")
	}
	return Choice{}, errors.New("model_not_available")
}
