package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"code.cloudfoundry.org/cli/cf/configuration/coreconfig"
	"code.cloudfoundry.org/cli/cf/models"
	"github.com/google/go-github/github"
	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"github.com/gorilla/sessions"
	"github.com/kelseyhightower/envconfig"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"
)

type Config struct {
	SecretKey     string `envconfig:"SECRET_KEY" required:"true"`
	SecureCookies bool   `envconfig:"SECURE_COOKIES" default:"true"`
	Hostname      string `envconfig:"HOSTNAME" required:"true"`
	ClientID      string `envconfig:"CLIENT_ID" required:"true"`
	ClientSecret  string `envconfig:"CLIENT_SECRET" required:"true"`
	AuthURL       string `envconfig:"AUTH_URL" required:"true"`
	TokenURL      string `envconfig:"TOKEN_URL" required:"true"`
	CFURL         string `envconfig:"CF_URL" required:"true"`
	Port          string `envconfig:"PORT" default:"3000"`
}

type EnvVar struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Value       string `yaml:"value"`
}

type Service struct {
	Service string                 `yaml:"service"`
	Plan    string                 `yaml:"plan"`
	Label   string                 `yaml:"label"`
	Tags    []string               `yaml:"tags"`
	Config  map[string]interface{} `yaml:"config"`
}

type AppWrapper struct {
	Deployment App `yaml:"deployment"`
}

type App struct {
	EnvVars  map[string]EnvVar `yaml:"env"`
	Services []Service         `yaml:"services"`
}

func LoadConfig(client *github.Client, owner, repo, ref string) (App, error) {
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

type OrgResponse struct {
	NextURL   string `json:"next_url"`
	Resources []Org  `json:"resources"`
}

type Org struct {
	Meta struct {
		GUID string `json:"guid"`
	} `json:"metadata"`
	Entity struct {
		Name string `json:"name"`
	} `json:"entity"`
}

type SpaceResponse struct {
	NextURL   string  `json:"next_url"`
	Resources []Space `json:"resources"`
}

type Space struct {
	Meta struct {
		GUID string `json:"guid"`
	} `json:"metadata"`
	Entity struct {
		OrgName string `json:"-"`
		OrgGUID string `json:"organization_guid"`
		Name    string `json:"name"`
	} `json:"entity"`
}

func FetchOrgs(client *http.Client, config Config) ([]Org, error) {
	orgs := []Org{}
	pageURL := "/v2/organizations"
	for {
		page, err := fetchOrganizationsPage(client, config.CFURL+pageURL)
		if err != nil {
			return []Org{}, err
		}
		orgs = append(orgs, page.Resources...)
		pageURL = page.NextURL
		if pageURL == "" {
			break
		}
	}
	return orgs, nil
}

func FetchSpaces(client *http.Client, config Config) ([]Space, error) {
	spaces := []Space{}
	pageURL := "/v2/spaces"
	for {
		page, err := fetchSpacesPage(client, config.CFURL+pageURL)
		if err != nil {
			return []Space{}, err
		}
		spaces = append(spaces, page.Resources...)
		pageURL = page.NextURL
		if pageURL == "" {
			break
		}
	}
	return spaces, nil
}

func fetchOrganizationsPage(client *http.Client, url string) (OrgResponse, error) {
	resp, err := client.Get(url)
	if err != nil {
		return OrgResponse{}, err
	}

	defer resp.Body.Close()
	page := OrgResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return OrgResponse{}, errors.New("")
	}

	return page, nil
}

func fetchSpacesPage(client *http.Client, url string) (SpaceResponse, error) {
	resp, err := client.Get(url)
	if err != nil {
		return SpaceResponse{}, err
	}

	defer resp.Body.Close()
	page := SpaceResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return SpaceResponse{}, errors.New("")
	}

	return page, nil
}

func FetchTargets(client *http.Client, config Config) ([]Space, error) {
	orgs, err := FetchOrgs(client, config)
	if err != nil {
		return []Space{}, err
	}

	spaces, err := FetchSpaces(client, config)
	if err != nil {
		return []Space{}, err
	}

	orgMap := map[string]string{}
	for _, org := range orgs {
		orgMap[org.Meta.GUID] = org.Entity.Name
	}

	for idx := range spaces {
		spaces[idx].Entity.OrgName = orgMap[spaces[idx].Entity.OrgGUID]
	}

	return spaces, nil
}

type Context struct {
	store       sessions.Store
	oauthConfig *oauth2.Config
	templates   *template.Template
	config      Config
}

type ContextHandler func(*Context, http.ResponseWriter, *http.Request)

func Contextify(c *Context, h ContextHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(c, w, r)
	})
}

func RequireAuth(context *Context, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := context.store.Get(r, "session")
		if _, ok := session.Values["token"].(oauth2.Token); ok {
			handler.ServeHTTP(w, r)
		} else {
			session.Values["redirect"] = r.URL.String()
			session.Save(r, w)
			http.Redirect(w, r, "/auth", http.StatusFound)
		}
	})
}

func Auth(c *Context, w http.ResponseWriter, r *http.Request) {
	session, _ := c.store.Get(r, "session")
	state, err := GenerateRandomString(32)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	session.Values["state"] = state
	err = session.Save(r, w)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, c.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOnline), http.StatusFound)
}

func Callback(c *Context, w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	session, _ := c.store.Get(r, "session")

	if state == "" || state != session.Values["state"] {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	redirect, ok := session.Values["redirect"].(string)
	if !ok {
		redirect = c.config.Hostname
	}

	token, err := c.oauthConfig.Exchange(oauth2.NoContext, code)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	session.Values["token"] = *token
	delete(session.Values, "state")
	delete(session.Values, "redirect")

	err = session.Save(r, w)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, redirect, http.StatusFound)
}

type Source struct {
	Owner string `schema:"owner,required"`
	Repo  string `schema:"repo,required"`
	Ref   string `schema:"ref,required"`
}

func Index(c *Context, w http.ResponseWriter, r *http.Request) {
	source := Source{}
	if err := schema.NewDecoder().Decode(&source, r.URL.Query()); err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	client := github.NewClient(nil)
	app, err := LoadConfig(client, source.Owner, source.Repo, source.Ref)
	if err != nil {
		fmt.Println(app, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	session, _ := c.store.Get(r, "session")
	token := session.Values["token"].(oauth2.Token)
	authClient := c.oauthConfig.Client(context.TODO(), &token)
	targets, err := FetchTargets(authClient, c.config)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	c.templates.ExecuteTemplate(w, "index.html", map[string]interface{}{
		csrf.TemplateTag: csrf.TemplateField(r),
		"App":            app,
		"Source":         source,
		"Targets":        targets,
	})
}

func Deploy(c *Context, w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	source := Source{}
	decoder := schema.NewDecoder()
	decoder.IgnoreUnknownKeys(true)
	if err := decoder.Decode(&source, r.Form); err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	target := strings.Split(r.Form.Get("target"), ":")
	fmt.Println(target)
	if len(target) != 4 {
		fmt.Println("invalid target", target)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	client := github.NewClient(nil)
	app, err := LoadConfig(client, source.Owner, source.Repo, source.Ref)
	if err != nil {
		fmt.Println(app, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	errors := []string{}
	for name, envvar := range app.EnvVars {
		envvar.Value = r.Form.Get(name)
		if envvar.Required && envvar.Value == "" {
			errors = append(errors, name)
		}
	}
	if len(errors) > 0 {
		fmt.Println(errors)
		w.WriteHeader(http.StatusBadRequest)
	}
	fmt.Println(app, errors)

	dir, err := ioutil.TempDir("", "")
	if err != nil {
		fmt.Println(dir, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)

	envPath := filepath.Join(dir, "env")
	appPath := filepath.Join(dir, "app")

	os.Mkdir(envPath, 0755)
	os.Mkdir(appPath, 0755)

	filename, err := download(client, appPath, source.Owner, source.Repo, source.Ref)
	if err != nil {
		fmt.Println(dir, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	tarPath := strings.TrimSuffix(filename, ".tar.gz")
	manifestPath := filepath.Join(appPath, tarPath, "manifest.yml")

	manifest, err := NewManifest(manifestPath)
	for name, envvar := range app.EnvVars {
		manifest.AddEnvironmentVariable(name, envvar.Value)
	}
	manifest.Save(manifestPath)

	session, _ := c.store.Get(r, "session")
	token := session.Values["token"].(oauth2.Token)

	cf := NewCloudFoundry(c.config, token, envPath, target[0], target[1], target[2], target[3])
	err = cf.writeConfig()

	route, err := cf.Create(app, manifestPath, filepath.Join(appPath, tarPath))
	fmt.Println(route, err)
}

func getArchiveURL(client *github.Client, user, repo, ref string) (string, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	url, _, err := client.Repositories.GetArchiveLink(user, repo, "tarball", opts)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

func download(client *github.Client, path, owner, repo, ref string) (string, error) {
	url, err := getArchiveURL(client, owner, repo, ref)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil {
		return "", err
	}

	return params["filename"], Untar(resp.Body, path)
}

func main() {
	config := Config{}
	if err := envconfig.Process("", &config); err != nil {
		log.Fatalf("Invalid configuration: %s", err.Error())
	}
	store := sessions.NewCookieStore([]byte(config.SecretKey))
	templates := template.Must(template.ParseFiles("index.html"))
	oauthConfig := &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  config.Hostname + "/callback",
		Scopes:       []string{"cloud_controller.read", "cloud_controller.write", "cloud_controller.admin", "openid"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  config.AuthURL + "/oauth/authorize",
			TokenURL: config.TokenURL + "/oauth/token",
		},
	}

	gob.Register(oauth2.Token{})

	ctx := &Context{
		config:      config,
		store:       store,
		oauthConfig: oauthConfig,
		templates:   templates,
	}

	r := mux.NewRouter()

	r.Path("/auth").Handler(Contextify(ctx, Auth))
	r.Path("/callback").Handler(Contextify(ctx, Callback))

	r.Path("/").Methods("GET").Handler(RequireAuth(ctx, Contextify(ctx, Index)))
	r.Path("/").Methods("POST").Handler(RequireAuth(ctx, Contextify(ctx, Deploy)))

	p := csrf.Protect([]byte(config.SecretKey), csrf.Secure(config.SecureCookies))
	http.ListenAndServe(":"+config.Port, p(r))
}

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

func (cf *CloudFoundry) writeConfig() error {
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

func (cf *CloudFoundry) Create(app App, manifest, path string) (string, error) {
	err := cf.createServices(app)
	if err != nil {
		return "", err
	}

	err = cf.createApp("testapp", manifest, path)
	if err != nil {
		return "", err
	}

	return cf.getRoute("testapp")
}

func (cf *CloudFoundry) createServices(app App) error {
	for _, service := range app.Services {
		err := cf.createService(service)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cf *CloudFoundry) createService(service Service) error {
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

	return cf.checkService(service, 30)
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
	args := []string{"push", app, "-f", manifest, "-p", path}
	return cf.cf(args...).Run()
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
