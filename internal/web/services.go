package web

// services.go — Service CRUD (operator-defined product tiers for the user
// wizard "Step 2 — What"). Mirrors the custom-preset pattern in presets.go:
// Services live in PanelSettings.Services (a json.RawMessage array), are
// round-tripped via Store.GetSettings/SaveSettings, and are referenced from a
// User via User.ServiceID. A Service bundles chains + per-chain exit pin +
// protocols + MTProxy defaults + (stored-not-rendered, P0b-3) routing-preset
// IDs. Deleting a Service referenced by any user is refused (serviceInUse).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

// handleServices renders the Service catalog page (all services are
// operator-defined — there are no built-in services).
func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, templates.ServicesPage(s.servicesList()))
}

func (s *Server) handleNewServiceForm(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	chains, _ := st.ListChains()
	s.render(w, r, templates.ServiceForm(nil, chains))
}

// handleCreateService accepts a new Service from the form. ID + Name required.
// ID must be unique among existing services (409 on collision).
func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	svc := serviceFromForm(r)
	if svc.ID == "" || svc.Name == "" {
		http.Error(w, i18n.T(r.Context(), "id and name are required"), http.StatusBadRequest)
		return
	}
	for _, existing := range s.servicesList() {
		if existing.ID == svc.ID {
			http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), fmt.Errorf("service %q already exists", svc.ID)), http.StatusConflict)
			return
		}
	}
	if err := s.saveService(svc, false); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "create", "service", svc.ID, chain.AuditPayload{"name": svc.Name}, "operator")
	s.render(w, r, templates.ServicesPage(s.servicesList()))
}

func (s *Server) handleEditServiceForm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	chains, _ := st.ListChains()
	for _, svc := range s.servicesList() {
		if svc.ID == id {
			s.render(w, r, templates.ServiceForm(&svc, chains))
			return
		}
	}
	http.Error(w, i18n.T(r.Context(), "service not found"), http.StatusNotFound)
}

// handleUpdateService replaces the Service with the given ID. The ID in the
// path is authoritative (the form's id field is ignored on update so a Service
// keeps a stable identity for User.ServiceID references).
func (s *Server) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	svc := serviceFromForm(r)
	svc.ID = id
	if svc.Name == "" {
		http.Error(w, i18n.T(r.Context(), "id and name are required"), http.StatusBadRequest)
		return
	}
	if err := s.saveService(svc, true); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "update", "service", svc.ID, chain.AuditPayload{"name": svc.Name}, "operator")
	s.render(w, r, templates.ServicesPage(s.servicesList()))
}

// handleDeleteService refuses deletion if any user references the Service
// (User.ServiceID == svc.ID); otherwise removes it from the array.
func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.store()
	if usedBy := serviceInUse(st, id); usedBy != "" {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "service in use"), usedBy), http.StatusConflict)
		return
	}
	settings, _ := st.GetSettings()
	var services []model.Service
	if len(settings.Services) > 0 {
		_ = json.Unmarshal(settings.Services, &services)
	}
	filtered := services[:0]
	for _, svc := range services {
		if svc.ID == id {
			continue
		}
		filtered = append(filtered, svc)
	}
	if len(filtered) == 0 {
		settings.Services = nil
	} else {
		b, _ := json.Marshal(filtered)
		settings.Services = b
	}
	st.SaveSettings(settings)
	chain.WriteAudit(st, "delete", "service", id, nil, "operator")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// serviceFromForm reads the Service fields from the form. Chains are repeated
// checkboxes (chains); per-chain exit pin via exit_<chainName> hidden/select;
// protocols repeated; routing preset ids repeated; MTProxy block.
func serviceFromForm(r *http.Request) model.Service {
	svc := model.Service{
		ID:          strings.TrimSpace(r.FormValue("id")),
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		ChainNames:  formList(r, "chains"),
		Protocols:   formList(r, "protocols"),
	}
	// Per-chain exit pin: exit_<chainName> form value carries the ChainNode.ID
	// to pin this Service's users to for that chain. Empty = chain default exit.
	svc.DefaultExitByChain = map[string]string{}
	for _, chainName := range svc.ChainNames {
		exit := strings.TrimSpace(r.FormValue("exit_" + chainName))
		if exit != "" {
			svc.DefaultExitByChain[chainName] = exit
		}
	}
	if len(svc.DefaultExitByChain) == 0 {
		svc.DefaultExitByChain = nil
	}
	svc.RoutingPresetIDs = formList(r, "routing_preset_ids")
	// MTProxy block.
	svc.MTProxy.Enabled = r.FormValue("mtproxy_enabled") == "on"
	svc.MTProxy.Domain = strings.TrimSpace(r.FormValue("mtproxy_domain"))
	svc.MTProxy.NodeIDs = formList(r, "mtproxy_nodes")
	svc.MTProxy.OrderIndex = atoi(r.FormValue("mtproxy_order_index"))
	if !svc.MTProxy.Enabled && svc.MTProxy.Domain == "" && len(svc.MTProxy.NodeIDs) == 0 && svc.MTProxy.OrderIndex == 0 {
		// leave the zero struct (omitempty drops it)
	}
	return svc
}

// saveService appends or replaces a Service in PanelSettings.Services by ID.
func (s *Server) saveService(svc model.Service, isUpdate bool) error {
	st := s.store()
	settings, _ := st.GetSettings()
	var services []model.Service
	if len(settings.Services) > 0 {
		_ = json.Unmarshal(settings.Services, &services)
	}
	replaced := false
	for i, existing := range services {
		if existing.ID == svc.ID {
			if !isUpdate {
				return fmt.Errorf("service %q already exists", svc.ID)
			}
			services[i] = svc
			replaced = true
			break
		}
	}
	if !replaced {
		services = append(services, svc)
	}
	b, err := json.Marshal(services)
	if err != nil {
		return err
	}
	settings.Services = b
	st.SaveSettings(settings)
	return nil
}

// servicesList unmarshals PanelSettings.Services. Returns an empty slice (not
// nil) when none are defined so the template can range safely.
func (s *Server) servicesList() []model.Service {
	settings, _ := s.store().GetSettings()
	var services []model.Service
	if len(settings.Services) > 0 {
		_ = json.Unmarshal(settings.Services, &services)
	}
	return services
}

// serviceInUse returns the name of the first user referencing the Service via
// User.ServiceID, or "" if unused. Drives the delete-refusal check.
func serviceInUse(st *chain.Store, id string) string {
	users, _ := st.ListUsers()
	for _, u := range users {
		if u.ServiceID == id {
			return u.Name
		}
	}
	return ""
}

// registerServiceRoutes wires the Service CRUD under /ui/services...
func (s *Server) registerServiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/services", s.auth(s.handleServices))
	mux.HandleFunc("GET /ui/services/new", s.auth(s.handleNewServiceForm))
	mux.HandleFunc("POST /ui/services", s.auth(s.handleCreateService))
	mux.HandleFunc("GET /ui/services/{id}/edit", s.auth(s.handleEditServiceForm))
	mux.HandleFunc("POST /ui/services/{id}/edit", s.auth(s.handleUpdateService))
	mux.HandleFunc("DELETE /ui/services/{id}", s.auth(s.handleDeleteService))
}