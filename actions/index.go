package actions

import (
	"context"
	"html/template"
	"log"
	"net/http"

	h "github.com/jmcarp/deploy-to-cf/helpers"

	"github.com/google/go-github/github"
	"github.com/gorilla/csrf"
	"github.com/gorilla/schema"
	"golang.org/x/oauth2"
)

func Index(c *h.Context, w http.ResponseWriter, r *http.Request) {
	source := Source{}
	if err := schema.NewDecoder().Decode(&source, r.URL.Query()); err != nil {
		log.Println(err)
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

	session, _ := c.Store.Get(r, "session")
	token := session.Values["token"].(oauth2.Token)
	authClient := c.OauthConfig.Client(context.TODO(), &token)
	targets, err := h.FetchTargets(authClient, c.Config)
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	c.Templates = template.Must(template.ParseFiles("templates/index.html", LayoutPath))
	c.Templates.ExecuteTemplate(w, "base", map[string]interface{}{
		csrf.TemplateTag: csrf.TemplateField(r),
		"App":            app,
		"Source":         source,
		"Targets":        targets,
		"Title":          "Home",
	})
}
