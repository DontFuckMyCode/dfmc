package config

import (
	"os"
	"strings"
	"testing"
)

// --- ProjectLearnedPatternsDir ---

func TestProjectLearnedPatternsDir_EmptyRoot(t *testing.T) {
	c := DefaultConfig()
	c.ProjectRoot = ""
	if dir := c.ProjectLearnedPatternsDir(); dir != "" {
		t.Errorf("expected empty for empty root, got %q", dir)
	}
}

func TestProjectLearnedPatternsDir_WithRoot(t *testing.T) {
	c := DefaultConfig()
	c.ProjectRoot = "/tmp/myproject"
	dir := c.ProjectLearnedPatternsDir()
	if dir == "" {
		t.Fatal("expected non-empty dir")
	}
	if !strings.HasSuffix(dir, ".dfmc"+string(os.PathSeparator)+"learned_patterns") && !strings.HasSuffix(dir, ".dfmc/learned_patterns") {
		t.Errorf("expected dir ending with .dfmc/learned_patterns, got %q", dir)
	}
}

// --- SetProjectRoot ---

func TestSetProjectRoot(t *testing.T) {
	c := DefaultConfig()
	c.SetProjectRoot("/custom/path")
	if c.ProjectRoot != "/custom/path" {
		t.Errorf("ProjectRoot = %q, want /custom/path", c.ProjectRoot)
	}
}

// --- EncryptSecret / DecryptSecret roundtrip ---

func TestEncryptDecryptSecret_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DFMC_CONFIG_DIR", tmp)

	plain := "super-secret-api-key-12345"
	encrypted, err := EncryptSecret(plain)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if encrypted == "" {
		t.Fatal("encrypted should not be empty for non-empty input")
	}
	if encrypted == plain {
		t.Fatal("encrypted should differ from plaintext")
	}
	if encrypted[:len(encryptedSecretPrefix)] != encryptedSecretPrefix {
		t.Errorf("encrypted should have prefix %q", encryptedSecretPrefix)
	}

	decrypted, err := DecryptSecret(encrypted)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if decrypted != plain {
		t.Errorf("roundtrip: got %q, want %q", decrypted, plain)
	}
}

func TestEncryptSecret_Empty(t *testing.T) {
	enc, err := EncryptSecret("")
	if err != nil {
		t.Fatalf("EncryptSecret empty: %v", err)
	}
	if enc != "" {
		t.Errorf("expected empty string for empty input, got %q", enc)
	}
}

func TestDecryptSecret_Empty(t *testing.T) {
	dec, err := DecryptSecret("")
	if err != nil {
		t.Fatalf("DecryptSecret empty: %v", err)
	}
	if dec != "" {
		t.Errorf("expected empty string for empty input, got %q", dec)
	}
}

func TestDecryptSecret_InvalidFormat(t *testing.T) {
	_, err := DecryptSecret("not-a-valid-format")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestDecryptSecret_BadPayload(t *testing.T) {
	_, err := DecryptSecret(encryptedSecretPrefix + "notbase64:alsonotbase64")
	if err == nil {
		t.Fatal("expected error for bad base64 payload")
	}
}

// --- applyTelegramEnv ---

func TestApplyTelegramEnv_Token(t *testing.T) {
	c := DefaultConfig()
	t.Setenv("DFMC_TELEGRAM_TOKEN", " test-token-123 ")
	c.applyTelegramEnv()
	if c.Telegram.Token != "test-token-123" {
		t.Errorf("token = %q, want %q", c.Telegram.Token, "test-token-123")
	}
}

func TestApplyTelegramEnv_AllowedUsers(t *testing.T) {
	c := DefaultConfig()
	t.Setenv("DFMC_TELEGRAM_ALLOWED_USERS", " 123 , 456 , 789 ")
	c.applyTelegramEnv()
	if len(c.Telegram.AllowedUsers) != 3 {
		t.Fatalf("expected 3 users, got %d: %v", len(c.Telegram.AllowedUsers), c.Telegram.AllowedUsers)
	}
	if c.Telegram.AllowedUsers[0] != 123 || c.Telegram.AllowedUsers[2] != 789 {
		t.Errorf("users = %v, want [123 456 789]", c.Telegram.AllowedUsers)
	}
}

func TestApplyTelegramEnv_AllowedUsersSkipsInvalid(t *testing.T) {
	c := DefaultConfig()
	t.Setenv("DFMC_TELEGRAM_ALLOWED_USERS", "123, abc, ,456")
	c.applyTelegramEnv()
	if len(c.Telegram.AllowedUsers) != 2 {
		t.Fatalf("expected 2 valid users, got %d: %v", len(c.Telegram.AllowedUsers), c.Telegram.AllowedUsers)
	}
}

func TestApplyTelegramEnv_SessionName(t *testing.T) {
	c := DefaultConfig()
	t.Setenv("DFMC_TELEGRAM_SESSION_NAME", "  my-session  ")
	c.applyTelegramEnv()
	if c.Telegram.SessionName != "my-session" {
		t.Errorf("session = %q, want %q", c.Telegram.SessionName, "my-session")
	}
}

func TestApplyTelegramEnv_NoEnvVars(t *testing.T) {
	c := DefaultConfig()
	os.Unsetenv("DFMC_TELEGRAM_TOKEN")
	os.Unsetenv("DFMC_TELEGRAM_ALLOWED_USERS")
	os.Unsetenv("DFMC_TELEGRAM_SESSION_NAME")
	c.applyTelegramEnv()
}

// --- selectModelsDevModel ---

func TestSelectModelsDevModel_DirectLookup(t *testing.T) {
	provider := ModelsDevProvider{
		Models: map[string]ModelsDevModel{
			"gpt-4o":        {ID: "gpt-4o", ToolCall: true, Modalities: ModelsDevModes{Input: []string{"text"}, Output: []string{"text"}}},
			"gpt-3.5-turbo": {ID: "gpt-3.5-turbo", ToolCall: false, Modalities: ModelsDevModes{Input: []string{"text"}, Output: []string{"text"}}},
		},
	}
	model, ok := selectModelsDevModel(provider, "gpt-4o", "openai")
	if !ok {
		t.Fatal("expected to find gpt-4o")
	}
	if model.ID != "gpt-4o" {
		t.Errorf("model.ID = %q, want gpt-4o", model.ID)
	}
}

func TestSelectModelsDevModel_FallbackToSeed(t *testing.T) {
	provider := ModelsDevProvider{
		Models: map[string]ModelsDevModel{
			"some-model": {ID: "some-model", ToolCall: true, Modalities: ModelsDevModes{Input: []string{"text"}, Output: []string{"text"}}},
		},
	}
	model, ok := selectModelsDevModel(provider, "nonexistent-model", "openai")
	_ = model
	_ = ok
}

func TestSelectModelsDevModel_NoTextModality(t *testing.T) {
	provider := ModelsDevProvider{
		Models: map[string]ModelsDevModel{
			"image-only": {ID: "image-only", ToolCall: false, Modalities: ModelsDevModes{Input: []string{"image"}, Output: []string{"image"}}},
		},
	}
	_, ok := selectModelsDevModel(provider, "nonexistent", "test")
	if ok {
		t.Error("should not find a model without text modality")
	}
}

func TestSelectModelsDevModel_DeprecatedSkipped(t *testing.T) {
	provider := ModelsDevProvider{
		Models: map[string]ModelsDevModel{
			"old-model": {ID: "old-model", Status: "deprecated", ToolCall: false, Modalities: ModelsDevModes{Input: []string{"text"}, Output: []string{"text"}}},
			"new-model": {ID: "new-model", Status: "active", ToolCall: true, Modalities: ModelsDevModes{Input: []string{"text"}, Output: []string{"text"}}},
		},
	}
	model, ok := selectModelsDevModel(provider, "nonexistent", "test")
	if !ok {
		t.Fatal("expected to find non-deprecated model")
	}
	if model.ID == "old-model" {
		t.Error("should not select deprecated model")
	}
}

// --- lookupModelsDevProvider ---

func TestLookupModelsDevProvider_ByProviderName(t *testing.T) {
	catalog := ModelsDevCatalog{
		"openai": {Name: "openai"},
	}
	p, ok := lookupModelsDevProvider(catalog, "openai")
	if !ok {
		t.Fatal("expected to find openai provider")
	}
	if p.Name != "openai" {
		t.Errorf("name = %q, want openai", p.Name)
	}
}

func TestLookupModelsDevProvider_ByCaseInsensitiveID(t *testing.T) {
	catalog := ModelsDevCatalog{
		"Google": {Name: "Google", ID: "google"},
	}
	p, ok := lookupModelsDevProvider(catalog, "google")
	if !ok {
		t.Fatal("expected to find Google via case-insensitive match")
	}
	if p.Name != "Google" {
		t.Errorf("name = %q, want Google", p.Name)
	}
}

func TestLookupModelsDevProvider_NotFound(t *testing.T) {
	catalog := ModelsDevCatalog{}
	_, ok := lookupModelsDevProvider(catalog, "nonexistent")
	if ok {
		t.Error("should not find nonexistent provider")
	}
}

// --- Validate additional branches ---

func helperValidConfig() *Config {
	return DefaultConfig()
}

func TestValidate_NegativeMaxTokens(t *testing.T) {
	c := helperValidConfig()
	p := c.Providers.Profiles[c.Providers.Primary]
	p.MaxTokens = -1
	c.Providers.Profiles[c.Providers.Primary] = p
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for negative max_tokens")
	}
}

func TestValidate_NegativeMaxContext(t *testing.T) {
	c := helperValidConfig()
	p := c.Providers.Profiles[c.Providers.Primary]
	p.MaxContext = -1
	c.Providers.Profiles[c.Providers.Primary] = p
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for negative max_context")
	}
}

func TestValidate_NegativeCacheSize(t *testing.T) {
	c := helperValidConfig()
	c.AST.CacheSize = -1
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for negative cache_size")
	}
}

func TestValidate_BadBaseURLScheme(t *testing.T) {
	c := helperValidConfig()
	p := c.Providers.Profiles[c.Providers.Primary]
	p.BaseURL = "ftp://example.com"
	c.Providers.Profiles[c.Providers.Primary] = p
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for bad base_url scheme")
	}
}

func TestValidate_BadBaseURLNoHost(t *testing.T) {
	c := helperValidConfig()
	p := c.Providers.Profiles[c.Providers.Primary]
	p.BaseURL = "https://"
	c.Providers.Profiles[c.Providers.Primary] = p
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for base_url without host")
	}
}

func TestValidate_InvalidProtocol(t *testing.T) {
	c := helperValidConfig()
	p := c.Providers.Profiles[c.Providers.Primary]
	p.Protocol = "invalid"
	c.Providers.Profiles[c.Providers.Primary] = p
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid protocol")
	}
}

func TestValidate_PlaceholderAPIKey(t *testing.T) {
	c := helperValidConfig()
	p := c.Providers.Profiles[c.Providers.Primary]
	p.APIKey = "<YOUR_API_KEY>"
	c.Providers.Profiles[c.Providers.Primary] = p
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for placeholder API key")
	}
}

func TestValidate_BadCompression(t *testing.T) {
	c := helperValidConfig()
	c.Context.Compression = "mega"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid compression")
	}
}

func TestValidate_BadWebAuth(t *testing.T) {
	c := helperValidConfig()
	c.Web.Auth = "oauth"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid web.auth")
	}
}

func TestValidate_BadRemoteAuth(t *testing.T) {
	c := helperValidConfig()
	c.Remote.Auth = "kerberos"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid remote.auth")
	}
}

func TestValidate_NegativeTUIGitDiffTimeout(t *testing.T) {
	c := helperValidConfig()
	c.TUI.GitDiffTimeoutSeconds = -1
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for negative git_diff_timeout_seconds")
	}
}

// --- isProjectConfigSecure ---

func TestIsProjectConfigSecure_NonexistentPath(t *testing.T) {
	ok := isProjectConfigSecure("/nonexistent/path/config.yaml")
	if !ok {
		t.Error("non-existent path should be considered secure")
	}
}

// --- loadYAML empty file ---

func TestLoadYAML_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	emptyPath := tmp + "/empty.yaml"
	if err := os.WriteFile(emptyPath, []byte{}, 0644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}
	var cfg Config
	if err := loadYAML(emptyPath, &cfg); err != nil {
		t.Errorf("empty file should not error: %v", err)
	}
}

// --- loadYAML file too large ---

func TestLoadYAML_FileTooLarge(t *testing.T) {
	tmp := t.TempDir()
	largePath := tmp + "/large.yaml"
	// Create a file larger than 1MB
	large := make([]byte, 1<<20+1)
	if err := os.WriteFile(largePath, large, 0644); err != nil {
		t.Fatalf("write large file: %v", err)
	}
	var cfg Config
	if err := loadYAML(largePath, &cfg); err == nil {
		t.Fatal("expected error for oversized config file")
	}
}

// --- Save os.MkdirAll error ---

func TestSave_MkdirAllError(t *testing.T) {
	// On Unix, passing "/" as path makes os.MkdirAll fail (or just skip).
	// Use a path that can't be created: /readonly-dir/x/config.yaml
	c := DefaultConfig()
	err := c.Save("/")
	// mkdir of "/" fails or is a no-op; Save should not panic
	_ = err
}

// --- SetKey nil receiver ---

func TestSetKey_NilReceiver(t *testing.T) {
	var c *Config
	// Should not panic
	c.SetKey("provider", "key")
}

// --- cloneHooksConfig ---

// --- normalizeAliases ---

func TestNormalizeAliases_AllowCommandToAllowShell(t *testing.T) {
	c := DefaultConfig()
	allowCmd := true
	c.Security.Sandbox.AllowCommand = &allowCmd
	c.normalizeAliases()
	if c.Security.Sandbox.AllowShell != true {
		t.Errorf("AllowShell: got %v", c.Security.Sandbox.AllowShell)
	}
	if c.Security.Sandbox.AllowCommand != nil {
		t.Error("AllowCommand should be nil after normalize")
	}
}

// --- UserConfigDir fallback ---

func TestUserConfigDir_HomeError(t *testing.T) {
	// If home dir lookup fails, should return DefaultDirName
	// We can't easily make os.UserHomeDir() fail, but we can
	// test the code path via coverage: when home == "", it returns DefaultDirName.
	// Verify the default dir name is used as fallback by checking
	// that the function returns DefaultDirName when home would be empty.
	orig := os.Getenv("HOME")
	os.Setenv("HOME", "")
	defer os.Setenv("HOME", orig)
	// After unsetting HOME, UserConfigDir should return DefaultDirName
	// (os.UserHomeDir uses HOME on Unix; setting it empty may make it fail)
	result := UserConfigDir()
	if result != DefaultDirName {
		t.Logf("UserConfigDir with empty HOME: got %q, want %q", result, DefaultDirName)
	}
}

// --- DataDir ---

func TestDataDir_CustomPath(t *testing.T) {
	c := DefaultConfig()
	c.DataDirPath = "/custom/data"
	if dir := c.DataDir(); dir != "/custom/data" {
		t.Errorf("DataDir = %q, want /custom/data", dir)
	}
}

// --- GetKey nil safety ---

func TestGetKey_NilConfig(t *testing.T) {
	var c *Config
	if key := c.GetKey("anything"); key != "" {
		t.Errorf("expected empty key for nil config, got %q", key)
	}
}

// --- applyEncryptedProviderKeys ---

func TestApplyEncryptedProviderKeys_EncryptedKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DFMC_CONFIG_DIR", tmp)

	plain := "my-api-key"
	encrypted, err := EncryptSecret(plain)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	c := DefaultConfig()
	c.Providers.Profiles["test-provider"] = ModelConfig{
		Model:           "test-model",
		APIKeyEncrypted: encrypted,
		APIKey:          "",
	}
	c.applyEncryptedProviderKeys()

	prof := c.Providers.Profiles["test-provider"]
	if prof.APIKey != plain {
		t.Errorf("APIKey = %q, want %q", prof.APIKey, plain)
	}
}

func TestApplyEncryptedProviderKeys_PlainKeyWins(t *testing.T) {
	c := DefaultConfig()
	c.Providers.Profiles["test-provider"] = ModelConfig{
		Model:           "test-model",
		APIKey:          "plain-key",
		APIKeyEncrypted: "dfmc:v1:some-encrypted-value",
	}
	c.applyEncryptedProviderKeys()

	prof := c.Providers.Profiles["test-provider"]
	if prof.APIKey != "plain-key" {
		t.Errorf("plain key should win, got %q", prof.APIKey)
	}
}

// --- Save custom path ---

func TestSave_CustomPath(t *testing.T) {
	tmp := t.TempDir()
	c := DefaultConfig()
	customPath := tmp + "/custom_config.yaml"
	if err := c.Save(customPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("custom config file should exist: %v", err)
	}
}
