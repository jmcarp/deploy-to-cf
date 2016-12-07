package actions

const LayoutPath string = "templates/layout.html"

type Source struct {
	Owner string `schema:"owner,required"`
	Repo  string `schema:"repo,required"`
	Ref   string `schema:"ref,required"`
}
