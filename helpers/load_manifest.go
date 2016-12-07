package helpers

import (
	"github.com/google/go-github/github"
	yaml "gopkg.in/yaml.v2"
)

type AppWrapper struct {
	Deployment App `yaml:"deployment"`
}

type App struct {
	EnvVars  map[string]*EnvVar `yaml:"env"`
	Services []Service          `yaml:"services"`
}

type Service struct {
	Service string                 `yaml:"service"`
	Plan    string                 `yaml:"plan"`
	Label   string                 `yaml:"label"`
	Tags    []string               `yaml:"tags"`
	Config  map[string]interface{} `yaml:"config"`
}

type EnvVar struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Value       string `yaml:"value"`
}

func LoadManifest(client *github.Client, owner, repo, ref string) (App, error) {
	wrapper := AppWrapper{}

	opts := &github.RepositoryContentGetOptions{Ref: ref}
	content, _, _, err := client.Repositories.GetContents(owner, repo, "manifest.yml", opts)
	if err != nil {
		return App{}, err
	}

	raw, err := content.GetContent()
	if err != nil {
		return App{}, err
	}

	if err := yaml.Unmarshal([]byte(raw), &wrapper); err != nil {
		return App{}, err
	}
	return wrapper.Deployment, nil
}
