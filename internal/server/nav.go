package server

import (
	"context"
	"sort"
)

type navResource struct {
	ID   string
	Name string
}

type navProject struct {
	Name      string
	Resources []navResource
}

// navTree groups resources by their project (group) for the sidebar. It is
// best-effort: a query failure yields an empty tree rather than breaking the page.
func (s *Server) navTree(ctx context.Context) []navProject {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil
	}
	byGroup := make(map[string][]navResource)
	var order []string
	for _, p := range projects {
		group := p.ProjectGroup
		if group == "" {
			group = "Default"
		}
		if _, ok := byGroup[group]; !ok {
			order = append(order, group)
		}
		byGroup[group] = append(byGroup[group], navResource{ID: p.ID, Name: p.Name})
	}
	sort.Strings(order)

	out := make([]navProject, 0, len(order))
	for _, g := range order {
		resources := byGroup[g]
		sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })
		out = append(out, navProject{Name: g, Resources: resources})
	}
	return out
}
