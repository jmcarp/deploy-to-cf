package actions

import (
	"fmt"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	h "github.com/jmcarp/deploy-to-cf/helpers"

	"github.com/google/go-github/github"
	"github.com/gorilla/schema"
	"golang.org/x/oauth2"
)

func Deploy(c *h.Context, w http.ResponseWriter, r *http.Request) {
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
	log.Println(target)
	if len(target) != 4 {
		log.Println("invalid target", target)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	client := github.NewClient(nil)
	app, err := h.LoadManifest(client, source.Owner, source.Repo, source.Ref)
	if err != nil {
		log.Println(app, err)
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
		log.Println(errors)
		w.WriteHeader(http.StatusBadRequest)
	}
	log.Println(app, errors)

	dir, err := ioutil.TempDir("", "")
	if err != nil {
		log.Println(dir, err)
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
		log.Println(dir, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	tarPath := strings.TrimSuffix(filename, ".tar.gz")
	manifestPath := filepath.Join(appPath, tarPath, "manifest.yml")

	manifest, err := h.NewManifest(manifestPath)
	for name, envvar := range app.EnvVars {
		manifest.AddEnvironmentVariable(name, envvar.Value)
	}
	manifest.Save(manifestPath)

	session, _ := c.Store.Get(r, "session")
	token := session.Values["token"].(oauth2.Token)

	cf := h.NewCloudFoundry(c.Config, token, envPath, target[0], target[1], target[2], target[3])
	err = cf.WriteConfig()

	route, err := cf.Create(app, manifestPath, filepath.Join(appPath, tarPath), c.Config.ServiceTimeout)
	log.Println(route, err)
}

func getArchiveURL(client *github.Client, user, repo, ref string) (string, error) {
	opts := &github.RepositoryContentGetOptions{Ref: ref}
	log.Println(user, repo, opts)
	url, foo, err := client.Repositories.GetArchiveLink(user, repo, "tarball", opts)
	log.Println(foo)
	if err != nil {
		return "", err
	}
	log.Println(url)
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

	return params["filename"], h.Untar(resp.Body, path)
}
