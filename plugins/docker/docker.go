package docker

import (
	"context"
	"fmt"

	"forge/internal/plugin"
)

func init() {
	plugin.Register(Plugin{})
}

type Plugin struct{}

func (Plugin) Name() string    { return "docker" }
func (Plugin) Version() string { return "0.1.0" }

func (Plugin) Tools() []plugin.Tool {
	return []plugin.Tool{
		{
			Name:        "sandbox_start",
			Description: "Start a Docker sandbox container for the model to use as a dev environment. Returns the container ID and shell command prefix.",
			Parameters: []plugin.ParameterDef{
				{Name: "image", Type: "string", Description: "Docker image to run (e.g. ubuntu:22.04)", Required: true, Default: "ubuntu:22.04"},
				{Name: "work_dir", Type: "string", Description: "Working directory inside the container", Required: false, Default: "/workspace"},
			},
			Execute: sandboxStart,
		},
		{
			Name:        "sandbox_stop",
			Description: "Stop and remove a Docker sandbox container.",
			Parameters: []plugin.ParameterDef{
				{Name: "container_id", Type: "string", Description: "Container ID returned by sandbox_start", Required: true},
			},
			Execute: sandboxStop,
		},
	}
}

func sandboxStart(ctx context.Context, args map[string]any) (string, error) {
	image := stringArg(args, "image", "ubuntu:22.04")
	workDir := stringArg(args, "work_dir", "/workspace")
	return fmt.Sprintf("sandbox container started (stub). image=%s workdir=%s\nUse: docker exec -it <container> bash", image, workDir), nil
}

func sandboxStop(ctx context.Context, args map[string]any) (string, error) {
	id := stringArg(args, "container_id", "")
	if id == "" {
		return "", fmt.Errorf("container_id is required")
	}
	return fmt.Sprintf("sandbox container %s stopped (stub)", id), nil
}

func stringArg(args map[string]any, key, fallback string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return fallback
}
