package helpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"code.cloudfoundry.org/cli/cf/commandregistry"
	"code.cloudfoundry.org/cli/cf/commandsloader"
	"code.cloudfoundry.org/cli/cf/configuration/coreconfig"
	"code.cloudfoundry.org/cli/cf/flags"
	"code.cloudfoundry.org/cli/cf/models"
	"code.cloudfoundry.org/cli/cf/requirements"
	"code.cloudfoundry.org/cli/cf/trace"
	"golang.org/x/oauth2"
)

type CloudFoundry struct {
	path string
	data coreconfig.Data
}

func NewCloudFoundry(config Config, token oauth2.Token, path, orgGUID, orgName, spaceGUID, spaceName string) *CloudFoundry {
	return &CloudFoundry{
		path: path,
		data: coreconfig.Data{
			Target:                config.CFURL,
			AuthorizationEndpoint: config.AuthURL,
			UaaEndpoint:           config.TokenURL,
			CFOAuthClient:         config.ClientID,
			CFOAuthClientSecret:   config.ClientSecret,
			AccessToken:           token.TokenType + " " + token.AccessToken,
			RefreshToken:          token.RefreshToken,
			OrganizationFields: models.OrganizationFields{
				GUID: orgGUID,
				Name: orgName,
			},
			SpaceFields: models.SpaceFields{
				GUID: spaceGUID,
				Name: spaceName,
			},
		},
	}
}

func (cf *CloudFoundry) WriteConfig() error {
	path := filepath.Join(cf.path, ".cf", "config.json")

	output, err := cf.data.JSONMarshalV3()
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Join(cf.path, ".cf"), 0755)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(path, output, 0644)
}

func (cf *CloudFoundry) Create(app App, manifest, path string, timeout int) (string, error) {
	err := cf.createServices(app, timeout)
	if err != nil {
		return "", err
	}

	err = cf.createApp("testapp", manifest, path)
	if err != nil {
		return "", err
	}

	return cf.getRoute("testapp")
}

func (cf *CloudFoundry) createServices(app App, timeout int) error {
	for _, service := range app.Services {
		err := cf.createService(service, timeout)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cf *CloudFoundry) createService(service Service, timeout int) error {
	args := []string{"create-service", service.Service, service.Plan, service.Label}
	if len(service.Tags) > 0 {
		args = append(args, "-t", strings.Join(service.Tags, ","))
	}
	if len(service.Config) > 0 {
		config, err := json.Marshal(service.Config)
		if err != nil {
			return err
		}
		args = append(args, "-c", string(config))
	}

	err := cf.cf(args...).Run()
	if err != nil {
		return err
	}

	return cf.checkService(service, timeout)
}

func (cf *CloudFoundry) checkService(service Service, timeout int) error {
	args := []string{"service", service.Label}
	elapsed := 0

	for {
		buf := bytes.Buffer{}
		cmd := cf.cf(args...)
		cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
		err := cmd.Run()

		if err == nil {
			output := buf.String()
			for _, line := range strings.Split(output, "\n") {
				if line == "Status: create succeeded" {
					return nil
				}
			}
		}

		elapsed += 5
		if elapsed > timeout {
			return fmt.Errorf("Service %s incomplete", service.Label)
		}

		time.Sleep(5 * time.Second)
	}
}

func (cf *CloudFoundry) createApp(app, manifest, path string) error {
	os.Setenv("CF_HOME", cf.path)
	defer os.Unsetenv("CF_HOME")

	traceLogger := trace.NewLogger(os.Stdout, false, "", "")

	deps := commandregistry.NewDependency(os.Stdout, traceLogger, os.Getenv("CF_DIAL_TIMEOUT"))
	defer deps.Config.Close()

	commandsloader.Load()
	cmd := commandregistry.Commands.FindCommand("push")
	cmd = cmd.SetDependency(deps, false)

	meta := cmd.MetaData()
	flagContext := flags.NewFlagContext(meta.Flags)
	flagContext.SkipFlagParsing(meta.SkipFlagParsing)
	flagContext.Parse(app, "-f", manifest, "-p", path)

	requirementsFactory := requirements.NewFactory(deps.Config, deps.RepoLocator)
	reqs, err := cmd.Requirements(requirementsFactory, flagContext)
	if err != nil {
		return err
	}

	for _, req := range reqs {
		err = req.Execute()
		if err != nil {
			return err
		}
	}

	return cmd.Execute(flagContext)
}

func (cf *CloudFoundry) cf(args ...string) *exec.Cmd {
	cmd := exec.Command("cf", args...)

	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "CF_COLOR=true", fmt.Sprintf("CF_HOME=%s", cf.path))

	return cmd
}

func (cf *CloudFoundry) getRoute(name string) (string, error) {
	buf := bytes.Buffer{}
	cmd := cf.cf("app", name)
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	output := strings.Split(buf.String(), "\n")
	for _, line := range output {
		if strings.Index(line, "urls: ") == 0 {
			return strings.Replace(line, "urls: ", "", 1), nil
		}
	}

	return "", fmt.Errorf("No URL found for app %s", name)
}
