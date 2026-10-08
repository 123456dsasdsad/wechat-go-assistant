package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

var reviewInlineID = regexp.MustCompile(`\[资料\s*(\d+)\]`)

// Recover an omitted machine-readable ID only when the review explicitly cites
// that same material inline and it belongs to the supplied corpus. Unknown IDs
// remain untouched so Publish rejects them.
func reconcileReviewCitations(snap library.Snapshot, r library.Review) library.Review {
	allowed := map[int64]bool{}
	for _, m := range snap.Materials {
		allowed[m.ID] = true
	}
	for i := range r.Sections {
		sec := &r.Sections[i]
		seen := map[int64]bool{}
		for _, id := range sec.MaterialIDs {
			seen[id] = true
		}
		for _, match := range reviewInlineID.FindAllStringSubmatch(sec.Text, -1) {
			id, _ := strconv.ParseInt(match[1], 10, 64)
			if allowed[id] && !seen[id] {
				sec.MaterialIDs = append(sec.MaterialIDs, id)
				seen[id] = true
			}
		}
	}
	return r
}

func (r *libraryRunner) publishReview(name string, input []byte, snap library.Snapshot, review library.Review) (library.Review, error) {
	for attempt := 0; attempt < 2; attempt++ {
		review.Topic = snap.Topic.Name
		review = reconcileReviewCitations(snap, review)
		review = preserveUnchanged(snap, review)
		if len(snap.Materials) > 0 {
			review = setReviewMatrix(review, snap.Materials)
		}
		var e error
		review, e = r.cfg.library.MergeLocked(r.task.Owner, snap.Topic.Name, review)
		if e != nil {
			return review, e
		}
		published, e := r.cfg.library.Publish(r.task.Owner, snap, review)
		if e == nil {
			return published, nil
		}
		cacheName := name
		if attempt == 1 {
			cacheName += "-repair"
		}
		os.Remove(filepath.Join(r.dir, cacheName+".json"))
		// One bounded correction with the actual validation error and evidence.
		// Publishing still uses exactly the same strict checks. Corpus races and
		// operational failures require a fresh snapshot instead of model repair.
		if attempt == 1 || !(strings.HasPrefix(e.Error(), "review_citation_missing") || e.Error() == "review_foreign_citation" || e.Error() == "review_inline_citation_invalid") {
			return review, e
		}
		draft, _ := json.Marshal(review)
		if e = r.stage(name+"-repair", `修正综述的引用结构，不能编造或随意补引用。校验错误：`+e.Error()+`。只使用输入方法卡的真实资料ID；有依据的段落用[资料 编号]并在material_ids中列出相同ID。不属于当前类别的ID必须删除。无依据的实质性结论删除；没有待补读文献等空章节可用空text和空material_ids，不必硬写论据。范围说明放“范围与覆盖”。锁定章节和用户笔记由程序原样保留。输出完整 {"topic":"类别","sections":[{"name":"章节","text":"中文正文","material_ids":[1]}],"changes":["变化"]}。待修正草稿：`+string(draft)+`\n真实输入证据：`+string(input), &review); e != nil {
			return review, e
		}
	}
	return review, errors.New("review_repair_failed")
}
