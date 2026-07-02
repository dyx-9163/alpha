package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNacosConfigRevisionEncryptsAndHidesContent(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	created, err := s.SaveNacosConfigRevision(NacosConfigRevision{
		NacosInstanceID: "nacos-1",
		Namespace:       "prod",
		Group:           "DEFAULT_GROUP",
		DataID:          "application-prod.yml",
		Content:         "spring:\n  datasource:\n    password: super-secret\n",
		CreatedBy:       "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Content != "" {
		t.Fatal("saved revision response must not include plaintext content")
	}
	if created.ContentHash == "" {
		t.Fatal("content hash is required")
	}

	list, err := s.ListNacosConfigRevisions(NacosConfigRevisionQuery{NacosInstanceID: "nacos-1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one revision, got %d", len(list))
	}
	if list[0].Content != "" {
		t.Fatal("revision list must not include plaintext content by default")
	}

	withContent, err := s.GetNacosConfigRevision(created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withContent.Content, "super-secret") {
		t.Fatalf("decrypted content = %q", withContent.Content)
	}

	var cipher string
	if err := s.db.QueryRow(`select content_cipher from nacos_config_revisions where id=?`, created.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipher, "super-secret") {
		t.Fatal("stored revision content must be encrypted")
	}
}

func TestNacosConfigRevisionsKeepLatestThree(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	base := time.Now().Add(-time.Hour)
	for index, content := range []string{"v1", "v2", "v3", "v4"} {
		if _, err := s.SaveNacosConfigRevision(NacosConfigRevision{
			NacosInstanceID: "nacos-1",
			Namespace:       "prod",
			Group:           "DEFAULT_GROUP",
			DataID:          "application-prod.yml",
			Content:         content,
			PublishedAt:     base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteOldNacosConfigRevisions("nacos-1", "prod", "DEFAULT_GROUP", "application-prod.yml", 3); err != nil {
		t.Fatal(err)
	}
	count, err := s.CountRows("nacos_config_revisions")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("revision count = %d, want 3", count)
	}
	list, err := s.ListNacosConfigRevisions(NacosConfigRevisionQuery{NacosInstanceID: "nacos-1", Limit: 3}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := list[0].Content; got != "v4" {
		t.Fatalf("latest content = %q, want v4", got)
	}
	if got := list[2].Content; got != "v2" {
		t.Fatalf("oldest kept content = %q, want v2", got)
	}
}
