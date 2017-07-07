package main

import (
	"encoding/gob"
	"html/template"
	"log"
	"net/http"
	"os"

	a "github.com/jmcarp/deploy-to-cf/actions"
	. "github.com/jmcarp/deploy-to-cf/helpers"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/kelseyhightower/envconfig"
	"golang.org/x/oauth2"
)

func main() {
	config := Config{}
	if err := envconfig.Process("", &config); err != nil {
		log.Fatalf("Invalid configuration: %s", err.Error())
	}
	store := sessions.NewFilesystemStore(os.TempDir(), []byte(config.SecretKey))
	store.MaxLength(8192)
	templates := template.Must(template.ParseFiles("templates/index.html"))
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

	if config.ButtonLogo != "" {
		err := WriteImage(config.ButtonLogo, "./static/button-logo.png")
		if err != nil {
			log.Fatalf("Error writing image: %s", err.Error())
		}
	}

	gob.Register(oauth2.Token{})

	ctx := &Context{
		Config:      config,
		Store:       store,
		OauthConfig: oauthConfig,
		Templates:   templates,
	}

	r := mux.NewRouter()

	r.Path("/auth").Handler(Contextify(ctx, Auth))
	r.Path("/callback").Handler(Contextify(ctx, Callback))

	r.Path("/").Methods("GET").Handler(RequireAuth(ctx, Contextify(ctx, a.Index)))
	r.Path("/").Methods("POST").Handler(RequireAuth(ctx, Contextify(ctx, a.Deploy)))

	r.PathPrefix("/static").Handler(http.StripPrefix("/static", http.FileServer(http.Dir("./static"))))

	p := csrf.Protect([]byte(config.SecretKey), csrf.Secure(config.SecureCookies))
	log.Println("Listening")
	http.ListenAndServe(":"+config.Port, p(r))
}
