package helpers

import (
	"encoding/json"
	"errors"
	"net/http"
)

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
