package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
	"github.com/minio/minio-go/v7"
)

const maxSavedAIResultBytes = 50 << 20

type aiRefreshStatus struct {
	Method     string `json:"method"`
	State      string `json:"state"`
	Scanned    int    `json:"scanned"`
	Updated    int    `json:"updated"`
	Current    int    `json:"current"`
	Failed     int    `json:"failed"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

type aiRefreshProgress struct {
	mu     sync.Mutex
	status aiRefreshStatus
}

func (s *server) aiReanalysisStatus(w http.ResponseWriter, _ *http.Request) {
	s.aiRefresh.mu.Lock()
	status := s.aiRefresh.status
	s.aiRefresh.mu.Unlock()
	if status.Method == "" {
		status.Method, status.State = domain.AIDetectionMethod, "pending"
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) saveAIRefreshStatus(status aiRefreshStatus) {
	s.aiRefresh.mu.Lock()
	s.aiRefresh.status = status
	s.aiRefresh.mu.Unlock()
	dir := filepath.Join(s.cfg.dataDir, "ai-reanalysis", domain.AIDetectionMethod)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("AI refresh progress: %v", err)
		return
	}
	body, err := json.Marshal(status)
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, "status.tmp")
	if err = os.WriteFile(tmp, body, 0o600); err == nil {
		err = os.Rename(tmp, filepath.Join(dir, "status.json"))
	}
	if err != nil {
		log.Printf("AI refresh progress: %v", err)
	}
}

// Object method stamps are the durable checkpoint, including after local workspaces are pruned.
func (s *server) refreshSavedAI(ctx context.Context) {
	if s.cfg.requireR2() != nil {
		s.saveAIRefreshStatus(aiRefreshStatus{Method: domain.AIDetectionMethod, State: "disabled", LastError: "R2 is not configured"})
		return
	}
	r2, err := newR2Client(ctx, s.cfg)
	if err != nil {
		s.saveAIRefreshStatus(aiRefreshStatus{Method: domain.AIDetectionMethod, State: "failed", LastError: err.Error()})
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		s.refreshSavedAIPass(ctx, r2)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
}

func (s *server) refreshSavedAIPass(ctx context.Context, r2 *r2Client) {
	s.activity.begin()
	defer s.activity.end()
	status := aiRefreshStatus{Method: domain.AIDetectionMethod, State: "running", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	s.saveAIRefreshStatus(status)
	prefix := s.cfg.aiResultPrefix + "/"
	for object := range r2.client.ListObjects(ctx, r2.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			status.Failed++
			status.LastError = object.Err.Error()
			break
		}
		rel := strings.TrimPrefix(object.Key, prefix)
		parts := strings.Split(rel, "/")
		if len(parts) != 2 || parts[1] != "result.json" {
			continue
		}
		if _, err := normalizeRunID(parts[0]); err != nil {
			continue
		}
		status.Scanned++
		if object.Size > maxSavedAIResultBytes {
			status.Failed++
			status.LastError = "saved result exceeds the 50 MiB limit"
			s.saveAIRefreshStatus(status)
			continue
		}
		select {
		case <-ctx.Done():
			status.State = "interrupted"
			s.saveAIRefreshStatus(status)
			return
		case s.gradeSlot <- struct{}{}:
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		changed, err := s.refreshSavedAIObject(runCtx, r2, object.Key)
		cancel()
		<-s.gradeSlot
		if err != nil {
			status.Failed++
			status.LastError = err.Error()
			log.Printf("AI refresh result=%s failed: %v", object.Key, err)
		} else if changed {
			status.Updated++
		} else {
			status.Current++
		}
		s.saveAIRefreshStatus(status)
	}
	status.State = "complete"
	if status.Failed > 0 {
		status.State = "retrying"
	}
	if ctx.Err() != nil {
		status.State = "interrupted"
	}
	status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	s.saveAIRefreshStatus(status)
	log.Printf("AI refresh method=%s state=%s scanned=%d updated=%d current=%d failed=%d", status.Method, status.State, status.Scanned, status.Updated, status.Current, status.Failed)
}

func (s *server) refreshSavedAIObject(ctx context.Context, r2 *r2Client, key string) (bool, error) {
	info, err := r2.client.StatObject(ctx, r2.bucket, key, minio.StatObjectOptions{})
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Size > maxSavedAIResultBytes || info.ETag == "" {
		return false, fmt.Errorf("saved result has invalid size or ETag")
	}
	getOpts := minio.GetObjectOptions{}
	if err := getOpts.SetMatchETag(info.ETag); err != nil {
		return false, err
	}
	obj, err := r2.client.GetObject(ctx, r2.bucket, key, getOpts)
	if err != nil {
		return false, err
	}
	defer obj.Close()
	body, err := io.ReadAll(io.LimitReader(obj, maxSavedAIResultBytes+1))
	if err != nil {
		return false, err
	}
	if len(body) > maxSavedAIResultBytes {
		return false, fmt.Errorf("saved result exceeds the 50 MiB limit")
	}
	updated, changed, err := engine.ReanalyzeSavedAIDetection(ctx, body, defaultCompareConfig)
	if err != nil || !changed {
		return false, err
	}
	if len(updated) > maxSavedAIResultBytes {
		return false, fmt.Errorf("refreshed result exceeds the 50 MiB limit")
	}
	// A content hash gives every distinct previous result a stable backup key.
	hash := sha256.Sum256(body)
	backupKey := path.Join(path.Dir(key), "ai-revisions", domain.AIDetectionMethod, hex.EncodeToString(hash[:])+".json")
	backupOpts := minio.PutObjectOptions{ContentType: "application/json", DisableMultipart: true, DisableContentSha256: true, SendContentMd5: true}
	backupOpts.SetMatchETagExcept("*")
	_, err = r2.client.PutObject(ctx, r2.bucket, backupKey, bytes.NewReader(body), int64(len(body)), backupOpts)
	if err != nil && minio.ToErrorResponse(err).Code != "PreconditionFailed" {
		return false, fmt.Errorf("backing up saved analysis: %w", err)
	}
	putOpts := minio.PutObjectOptions{ContentType: "application/json", CacheControl: "private, no-store", DisableMultipart: true, DisableContentSha256: true, SendContentMd5: true}
	putOpts.SetMatchETag(info.ETag)
	if _, err := r2.client.PutObject(ctx, r2.bucket, key, bytes.NewReader(updated), int64(len(updated)), putOpts); err != nil {
		return false, fmt.Errorf("saving refreshed analysis (will retry): %w", err)
	}
	return true, nil
}
