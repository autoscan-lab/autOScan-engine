package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	policyFileName = "policy.yml"
)

type config struct {
	dataDir      string
	port         string
	engineSecret string
	// Zero disables idle exit (local runs).
	idleExit time.Duration

	r2AccountID  string
	r2AccessKey  string
	r2SecretKey  string
	r2BucketName string
	// Overrides the R2 endpoint, e.g. http://s3:8333 for the local SeaweedFS store.
	r2Endpoint     string
	aiResultPrefix string
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

	idleExit, _ := time.ParseDuration(strings.TrimSpace(os.Getenv("AUTOSCAN_IDLE_EXIT")))
	aiPrefix := strings.Trim(strings.TrimSpace(os.Getenv("R2_APP_PREFIX")), "/")
	if aiPrefix == "" {
		aiPrefix = "web"
	}
	aiResultPrefix := strings.Trim(strings.TrimSpace(os.Getenv("AUTOSCAN_AI_RESULT_PREFIX")), "/")
	if aiResultPrefix == "" {
		aiResultPrefix = aiPrefix + "/runs"
	}

	return config{
		dataDir:        dataDir,
		port:           port,
		engineSecret:   os.Getenv("ENGINE_SECRET"),
		idleExit:       idleExit,
		r2AccountID:    os.Getenv("R2_ACCOUNT_ID"),
		r2AccessKey:    os.Getenv("R2_ACCESS_KEY_ID"),
		r2SecretKey:    os.Getenv("R2_SECRET_ACCESS_KEY"),
		r2BucketName:   os.Getenv("R2_BUCKET_NAME"),
		r2Endpoint:     strings.TrimSpace(os.Getenv("R2_ENDPOINT")),
		aiResultPrefix: aiResultPrefix,
	}
}

func (c config) requireSecret() error {
	if c.engineSecret == "" {
		return fmt.Errorf("missing required environment variable: ENGINE_SECRET")
	}
	return nil
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
