package main

import (
	"net/http"

	. "github.com/jmcarp/deploy-to-cf/helpers"

	"golang.org/x/oauth2"
)

func Contextify(c *Context, h ContextHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(c, w, r)
	})
}

func RequireAuth(context *Context, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := context.Store.Get(r, "session")
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
	session, _ := c.Store.Get(r, "session")
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

	http.Redirect(w, r, c.OauthConfig.AuthCodeURL(state, oauth2.AccessTypeOnline), http.StatusFound)
}

func Callback(c *Context, w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	session, _ := c.Store.Get(r, "session")

	if state == "" || state != session.Values["state"] {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	redirect, ok := session.Values["redirect"].(string)
	if !ok {
		redirect = c.Config.Hostname
	}

	token, err := c.OauthConfig.Exchange(oauth2.NoContext, code)
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
