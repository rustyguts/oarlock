package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Artifacts: user-uploaded source files and container step outputs. Bytes live
// in the object store; this exposes upload/download/list/delete. All
// workspace-scoped (hard rule 3).

const maxUploadMemory = 32 << 20 // 32MB buffered; larger spills to temp files

type artifactRow struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type"`
	Source      string    `json:"source"`
	CreatedAt   string    `json:"created_at"`
}

func (s *Server) uploadArtifact(w http.ResponseWriter, r *http.Request) {
	if s.Artifacts == nil {
		s.error(w, http.StatusServiceUnavailable, fmt.Errorf("artifact store not configured"))
		return
	}
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("expected multipart form with a 'file' field"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("missing 'file'"))
		return
	}
	defer file.Close()

	ws := s.workspace(r)
	id := uuid.New()
	name := path.Base(header.Filename)
	if name == "" || name == "." {
		name = "upload"
	}
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(name))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	key := s.Artifacts.UploadKey(ws, id, name)
	if err := s.Artifacts.Upload(r.Context(), key, file, header.Size, ct); err != nil {
		s.error(w, http.StatusBadGateway, err)
		return
	}
	if _, err := s.DB.Exec(r.Context(), `
		insert into artifacts (id, workspace_id, key, name, size, content_type, source)
		values ($1, $2, $3, $4, $5, $6, 'upload')`,
		id, ws, key, name, header.Size, ct); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	s.json(w, http.StatusCreated, artifactRow{
		ID: id, Name: name, Size: header.Size, ContentType: ct, Source: "upload",
	})
}

func (s *Server) downloadArtifact(w http.ResponseWriter, r *http.Request) {
	if s.Artifacts == nil {
		s.error(w, http.StatusServiceUnavailable, fmt.Errorf("artifact store not configured"))
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid artifact id"))
		return
	}
	var key, name, ct string
	var size int64
	err = s.DB.QueryRow(r.Context(), `
		select key, name, content_type, size from artifacts
		where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&key, &name, &ct, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("artifact not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	rc, err := s.Artifacts.Download(r.Context(), key)
	if err != nil {
		s.error(w, http.StatusBadGateway, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, name))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	_, _ = io.Copy(w, rc)
}

func (s *Server) listRunArtifacts(w http.ResponseWriter, r *http.Request) {
	runID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid run id"))
		return
	}
	rows, err := s.DB.Query(r.Context(), `
		select id, name, size, content_type, source, created_at::text
		from artifacts where run_id = $1 and workspace_id = $2
		order by created_at`, runID, s.workspace(r))
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := []artifactRow{}
	for rows.Next() {
		var a artifactRow
		if err := rows.Scan(&a.ID, &a.Name, &a.Size, &a.ContentType, &a.Source, &a.CreatedAt); err != nil {
			s.error(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, a)
	}
	s.json(w, http.StatusOK, out)
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	if s.Artifacts == nil {
		s.error(w, http.StatusServiceUnavailable, fmt.Errorf("artifact store not configured"))
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.error(w, http.StatusBadRequest, fmt.Errorf("invalid artifact id"))
		return
	}
	var key string
	err = s.DB.QueryRow(r.Context(),
		`select key from artifacts where id = $1 and workspace_id = $2`, id, s.workspace(r)).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		s.error(w, http.StatusNotFound, fmt.Errorf("artifact not found"))
		return
	}
	if err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.Artifacts.Delete(r.Context(), key)
	if _, err := s.DB.Exec(r.Context(),
		`delete from artifacts where id = $1 and workspace_id = $2`, id, s.workspace(r)); err != nil {
		s.error(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
