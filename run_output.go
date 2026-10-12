package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
)

const runOutputSchema = `
CREATE TABLE IF NOT EXISTS run_context(run_id TEXT PRIMARY KEY REFERENCES runs(id),value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS output_images(id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id),run_id TEXT NOT NULL REFERENCES runs(id),source TEXT NOT NULL,mime TEXT NOT NULL,data BLOB NOT NULL,width INTEGER NOT NULL,height INTEGER NOT NULL,created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS output_images_run ON output_images(run_id,created,id);
`

// Context occupancy is a latest-value gauge, never billed token usage.
type RunContext struct {
	Used int64 `json:"used"`
	Size int64 `json:"size"`
}

func (c RunContext) valid() bool {
	return c.Used >= 0 && c.Size > 0 && c.Size <= 1<<40 && c.Used <= 1<<40
}

type OutputImage struct {
	ID     string `json:"id"`
	Mime   string `json:"mime"`
	Size   int    `json:"size"`
	Source string `json:"source"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}
type harnessImage struct {
	Type   string `json:"type"`
	Data   string `json:"data"`
	Mime   string `json:"mimeType"`
	Source string `json:"source"`
}

const outputImageLimit = 2 * 1024 * 1024
const outputImageCount = 12

func decodeOutputImage(v harnessImage) ([]byte, image.Config, error) {
	if v.Type != "image" || (v.Source != "assistant" && v.Source != "tool" && v.Source != "process") || len(v.Data) > base64.StdEncoding.EncodedLen(outputImageLimit) {
		return nil, image.Config{}, errors.New("图片类型或大小不受支持")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(v.Data)
	if err != nil || len(data) == 0 || len(data) > outputImageLimit {
		return nil, image.Config{}, errors.New("图片编码无效或超过 2 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	mimes := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}
	if err != nil || mimes[format] == "" || mimes[format] != v.Mime || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 32000000 {
		return nil, image.Config{}, errors.New("图片格式或尺寸无效（仅 PNG、JPEG、GIF、WebP）")
	}
	return data, cfg, nil
}

func (s *Store) saveOutputImage(task, run, raw string) error {
	var v harnessImage
	if json.Unmarshal([]byte(raw), &v) != nil {
		return errors.New("图片内容无效")
	}
	data, cfg, err := decodeOutputImage(v)
	if err != nil {
		return err
	}
	// Single statement enforces the per-run limit and task/run ownership.
	result, err := s.Exec(`INSERT INTO output_images SELECT ?,?,?,?, ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM runs WHERE id=? AND task_id=?) AND (SELECT count(*) FROM output_images WHERE run_id=?)<?`, uid(), task, run, v.Source, v.Mime, data, cfg.Width, cfg.Height, now(), run, task, run, outputImageCount)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("本轮图片已达 12 张或运行记录无效")
	}
	return nil
}

func (s *Server) outputImageRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/tasks/{id}/runs/{run}/images/{image}", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var kind string
		var data []byte
		if s.app.store.QueryRow("SELECT mime,data FROM output_images WHERE id=? AND task_id=? AND run_id=?", r.PathValue("image"), r.PathValue("id"), r.PathValue("run")).Scan(&kind, &data) != nil {
			fail(w, 404, "图片不存在")
			return
		}
		w.Header().Set("Content-Type", kind)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		if r.URL.Query().Get("download") == "1" {
			ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[kind]
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "output." + ext}))
		}
		_, _ = w.Write(data)
	}))
}
