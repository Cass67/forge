package bootstrap

import (
	"os"
	"path/filepath"

	"forge/internal/fsutil"
)

const providerTemplateName = "example.toml"

const providerTemplateContent = `# Example custom provider definition for Forge.
# Copy this file, rename it, and uncomment the block below to add a provider.
#
# [model_providers.my_provider]
# name = "My Provider"
# base_url = "https://example.com/v1"
# wire_api = "responses"
# http_headers = { client = "forge" }
# default_model = "gpt-5.4"
# models = ["gpt-5.4", "gpt-5.4-mini"]
#
# Models that accept image input (screenshots/attachments). Anything not
# listed gets images replaced with a text placeholder instead of failing.
# image_models = ["gpt-5.4"]
`

func ensureDefaultConfigScaffold() error {
	return ensureConfigScaffold(fsutil.ForgeConfigDir())
}

func ensureConfigScaffold(configDir string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}

	providersDir := filepath.Join(configDir, "providers")
	if err := os.MkdirAll(providersDir, 0o700); err != nil {
		return err
	}

	templatePath := filepath.Join(providersDir, providerTemplateName)
	if _, err := os.Stat(templatePath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(templatePath, []byte(providerTemplateContent), 0o600)
}
