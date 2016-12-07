package helpers

import (
	"encoding/base64"
	"html/template"
	"image"
	"image/png"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"
)

type Config struct {
	SecretKey      string `envconfig:"SECRET_KEY" required:"true"`
	SecureCookies  bool   `envconfig:"SECURE_COOKIES" default:"true"`
	Hostname       string `envconfig:"HOSTNAME" required:"true"`
	ClientID       string `envconfig:"CLIENT_ID" required:"true"`
	ClientSecret   string `envconfig:"CLIENT_SECRET" required:"true"`
	AuthURL        string `envconfig:"AUTH_URL" required:"true"`
	TokenURL       string `envconfig:"TOKEN_URL" required:"true"`
	CFURL          string `envconfig:"CF_URL" required:"true"`
	ServiceTimeout int    `envconfig:"SERVICE_TIMEOUT" default:"600"`
	Port           string `envconfig:"PORT" default:"3000"`
	ButtonLogo     string `envconfig:"BUTTON_LOGO"`
}

type Context struct {
	Store       sessions.Store
	OauthConfig *oauth2.Config
	Templates   *template.Template
	Config      Config
}

type ContextHandler func(*Context, http.ResponseWriter, *http.Request)

func WriteImage(data string, path string) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))
	m, _, err := image.Decode(reader)
	if err != nil {
		return err
	}

	return png.Encode(out, m)
}
