package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
)

func (r *libraryRunner) reviewInput(name string, snap library.Snapshot) ([]byte, error) {
	b, _ := json.Marshal(snap)
	if len(b) <= 96<<10 {
		return b, nil
	}
	groups := []library.Section{}
	for start := 0; start < len(snap.Materials); start += 12 {
		end := min(start+12, len(snap.Materials))
		part := snap.Materials[start:end]
		input, _ := json.Marshal(part)
		var group library.Section
		if e := r.stage(fmt.Sprintf("%s-evidence-%d", name, start/12), `为类别整理这组文献的方法证据，保留每篇独立编号、阅读范围、实验条件、结论/分歧和缺口。不能合并不可比数值，不遗漏任何资料。输出 {"name":"证据组","text":"带[资料 编号]的中文方法提要","material_ids":[每一条资料ID]}。输入：`+string(input), &group); e != nil {
			return nil, e
		}
		seen := map[int64]bool{}
		for _, id := range group.MaterialIDs {
			seen[id] = true
		}
		for _, m := range part {
			if !seen[m.ID] {
				return nil, errors.New("review_group_omitted_material")
			}
		}
		groups = append(groups, group)
	}
	index := []library.Material{}
	for _, m := range snap.Materials {
		index = append(index, library.Material{ID: m.ID, Title: m.Title, Revision: m.Revision, Sources: m.Sources, Missing: m.Missing})
	}
	return json.Marshal(map[string]any{"topic": snap.Topic, "previous": snap.Previous, "materials": index, "evidence_groups": groups})
}

func preserveUnchanged(snap library.Snapshot, r library.Review) library.Review {
	changed := map[int64]bool{}
	old := map[int64]int64{}
	current := map[int64]int64{}
	for _, m := range snap.Previous.References {
		old[m.ID] = m.Revision
	}
	for _, m := range snap.Materials {
		current[m.ID] = m.Revision
		if old[m.ID] != m.Revision {
			changed[m.ID] = true
		}
	}
	for id, rev := range old {
		if current[id] != rev {
			changed[id] = true
		}
	}
	for _, previous := range snap.Previous.Sections {
		if previous.Name == "范围与覆盖" || len(previous.MaterialIDs) == 0 {
			continue
		}
		touched := false
		for _, id := range previous.MaterialIDs {
			if changed[id] {
				touched = true
			}
		}
		if touched {
			continue
		}
		for i, section := range r.Sections {
			if section.Name != previous.Name {
				continue
			}
			for _, id := range section.MaterialIDs {
				if changed[id] {
					touched = true
				}
			}
			if !touched {
				r.Sections[i] = previous
			}
			break
		}
	}
	return r
}

func setReviewMatrix(r library.Review, materials []library.Material) library.Review {
	matrix := library.Matrix(materials)
	for i, s := range r.Sections {
		if s.Name == matrix.Name {
			r.Sections[i] = matrix
			return r
		}
	}
	r.Sections = append(r.Sections, matrix)
	return r
}
