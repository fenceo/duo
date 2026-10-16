package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApiCredentialValidateAndFingerprint(t *testing.T) {
	c := ApiCredential{ID: "cred-1", Name: "DeepSeek", Provider: "deepseek", Protocol: "openai-chat", BaseURL: "https://api.deepseek.com/v1", AuthMode: "api-key", Secret: "sk-synthetic"}
	if err := validateApiCredential(c); err != nil {
		t.Fatal(err)
	}
	fp := apiCredentialFingerprint(c.Secret)
	if len(fp) != 12 || fp == c.Secret || fp == apiCredentialFingerprint("other") {
		t.Fatal("fingerprint", fp)
	}
	if err := validateApiCredential(ApiCredential{ID: "x", Name: "bad", Protocol: "openai-chat", AuthMode: "api-key"}); err == nil {
		t.Fatal("empty secret accepted")
	}
	if err := validateApiCredential(ApiCredential{ID: "x", Name: "bad", Protocol: "nope", AuthMode: "api-key", Secret: "k"}); err == nil {
		t.Fatal("bad protocol accepted")
	}
}

func TestApiCredentialStoreAndListHidesSecret(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	cred := ApiCredential{ID: "cred-2", Name: "Custom", Provider: "custom", Protocol: "openai-chat", BaseURL: "https://relay.example/v1", AuthMode: "api-key", Secret: "sk-secret-value", Created: 1, Updated: 2}
	if err := a.store.saveApiCredentials([]ApiCredential{cred}); err != nil {
		t.Fatal(err)
	}
	got, err := a.store.apiCredential("cred-2")
	if err != nil || got.Secret != "sk-secret-value" {
		t.Fatal(got, err)
	}
	raw, _ := json.Marshal(got.view())
	if strings.Contains(string(raw), "sk-secret-value") {
		t.Fatal("view leaked secret", string(raw))
	}
	if !strings.Contains(string(raw), "key_fingerprint") {
		t.Fatal("missing fingerprint", string(raw))
	}
}
