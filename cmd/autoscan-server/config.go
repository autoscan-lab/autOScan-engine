package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	policyFileName = "policy.yml"
)

type config struct {
	dataDir      string
	currentDir   string
	port         string
	engineSecret string

	r2AccountID  string
	r2AccessKey  string
	r2SecretKey  string
	r2BucketName string
}

func loadConfig() config {
	dataDir := strings.TrimSpace(os.Getenv("AUTOSCAN_DATA_DIR"))
	if dataDir == "" {
		dataDir = "/data"
	}

	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	return config{
		dataDir:      dataDir,
		currentDir:   filepath.Join(dataDir, "current"),
		port:         port,
		engineSecret: os.Getenv("ENGINE_SECRET"),
		r2AccountID:  os.Getenv("R2_ACCOUNT_ID"),
		r2AccessKey:  os.Getenv("R2_ACCESS_KEY_ID"),
		r2SecretKey:  os.Getenv("R2_SECRET_ACCESS_KEY"),
		r2BucketName: os.Getenv("R2_BUCKET_NAME"),
	}
}

func (c config) requireR2() error {
	missing := []string{}
	if c.r2AccountID == "" {
		missing = append(missing, "R2_ACCOUNT_ID")
	}
	if c.r2AccessKey == "" {
		missing = append(missing, "R2_ACCESS_KEY_ID")
	}
	if c.r2SecretKey == "" {
		missing = append(missing, "R2_SECRET_ACCESS_KEY")
	}
	if c.r2BucketName == "" {
		missing = append(missing, "R2_BUCKET_NAME")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}
