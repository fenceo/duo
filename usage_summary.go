package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"
	_ "time/tzdata" // IANA day boundaries also work on Windows without system tzdata.
)

type UsageTotals struct {
	Runs     int64 `json:"runs"`
	Reported int64 `json:"reported"`
	Missing  int64 `json:"missing"`
	Running  int64 `json:"running"`
	Input    int64 `json:"input"`
	Output   int64 `json:"output"`
	Total    int64 `json:"total"`
}
type UsageModel struct {
	Model   string   `json:"model"`
	Engines []string `json:"engines"`
	UsageTotals
}
type UsageDay struct {
	Date   string       `json:"date"`
	Models []UsageModel `json:"models"`
	UsageTotals
}
type UsageReport struct {
	Timezone  string      `json:"timezone"`
	Generated int64       `json:"generated"`
	Total     UsageTotals `json:"total"`
	Period    UsageTotals `json:"period"`
	Days      []UsageDay  `json:"days"`
}

func (t *UsageTotals) add(raw, status string) {
	t.Runs++
	if status == "running" {
		t.Running++
	}
	var u struct {
		Input  *int64 `json:"input"`
		Output *int64 `json:"output"`
		Total  *int64 `json:"total"`
	}
	if json.Unmarshal([]byte(raw), &u) != nil || u.Input == nil || u.Output == nil || u.Total == nil || *u.Input < 0 || *u.Output < 0 || *u.Total < *u.Output || *u.Total-*u.Output < *u.Input {
		t.Missing++
		return
	}
	t.Reported++
	// Total-output is inclusive input for both Codex and Claude/Harness.
	in := *u.Total - *u.Output
	t.Input += in
	t.Output += *u.Output
	t.Total += *u.Total
}

func (s *Store) usageSummary(ctx context.Context, task string, days int, zone *time.Location, at time.Time) (UsageReport, error) {
	report := UsageReport{Timezone: zone.String(), Generated: at.UnixMilli(), Days: make([]UsageDay, days)}
	byDay := map[string]int{}
	byModel := make([]map[string]*UsageModel, days)
	today := at.In(zone)
	for i := range days {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		report.Days[i].Date = date
		byDay[date] = i
		byModel[i] = map[string]*UsageModel{}
		report.Days[i].Models = []UsageModel{}
	}
	// Read only small metric rows, independent of conversation pagination. This
	// endpoint runs on demand, never as part of the one-second task polling.
	query := `SELECT r.status,COALESCE(NULLIF(m.started,0),r.created),COALESCE(m.usage,'null'),
		COALESCE(json_extract(x.snapshot,'$.engine'),''),COALESCE(json_extract(x.snapshot,'$.model'),'')
		FROM runs r LEFT JOIN run_metrics m ON m.run_id=r.id LEFT JOIN run_execution x ON x.run_id=r.id
		WHERE NOT EXISTS(SELECT 1 FROM run_fork_sources f WHERE f.run_id=r.id)
		AND r.status<>'queued' AND (r.status<>'interrupted' OR COALESCE(m.started,0)>0 OR COALESCE(m.usage,'null') NOT IN ('','null'))`
	args := []any{}
	if task != "" {
		query += " AND r.task_id=?"
		args = append(args, task)
	}
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var status, raw, engine, model string
		var started int64
		if err = rows.Scan(&status, &started, &raw, &engine, &model); err != nil {
			return report, err
		}
		report.Total.add(raw, status)
		if index, ok := byDay[time.UnixMilli(started).In(zone).Format("2006-01-02")]; ok {
			report.Period.add(raw, status)
			report.Days[index].add(raw, status)
			key := model
			if key == "" {
				key = "\x00" + engine
			}
			group := byModel[index][key]
			if group == nil {
				group = &UsageModel{Model: model, Engines: []string{}}
				byModel[index][key] = group
			}
			found := false
			for _, existing := range group.Engines {
				if existing == engine {
					found = true
				}
			}
			if !found {
				group.Engines = append(group.Engines, engine)
			}
			group.add(raw, status)
		}
	}
	for i, groups := range byModel {
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			group := groups[key]
			sort.Strings(group.Engines)
			report.Days[i].Models = append(report.Days[i].Models, *group)
		}
	}
	return report, rows.Err()
}

func (s *Server) usageSummary(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 90 {
			fail(w, 400, "每日统计范围须为 1–90 天")
			return
		}
		days = n
	}
	name := r.URL.Query().Get("timezone")
	if name == "" {
		name = "UTC"
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		fail(w, 400, "无效的统计时区")
		return
	}
	if task := r.URL.Query().Get("task_id"); task != "" {
		if _, err = s.app.store.task(task); err != nil {
			fail(w, 404, "任务不存在")
			return
		}
	}
	report, err := s.app.store.usageSummary(r.Context(), r.URL.Query().Get("task_id"), days, zone, time.Now())
	if err != nil {
		fail(w, 500, "读取用量失败")
		return
	}
	jsonOut(w, 200, report)
}
