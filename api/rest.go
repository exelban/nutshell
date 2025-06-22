package api

import (
	"fmt"
	"log"
	"net/http"
	"nutshell/pkg"
	"nutshell/pkg/nut"
	"strings"
	"time"
)

type Rest struct {
	Version  string
	Template *pkg.Template
	Clients  []*nut.Client
}

func (s *Rest) Router() *http.ServeMux {
	router := NewRouter(Recoverer, CORS, Healthz, Info("NutGUI", s.Version))

	router.HandleFunc("GET /", s.list)
	router.HandleFunc("GET /{id}", s.details)
	router.HandleFunc("GET /static/", s.static)

	return router.mux
}

func (s *Rest) notFound(w http.ResponseWriter, r *http.Request) {
	if err := s.Template.NotFound.Execute(w, nil); err != nil {
		log.Printf("[ERROR] generate not found html: %v", err)
		http.Error(w, fmt.Sprintf("error generate not found html: %v", err), http.StatusInternalServerError)
	}
}

func (s *Rest) list(w http.ResponseWriter, r *http.Request) {
	type ups struct {
		ID             string
		Name           string
		Status         string
		OriginalStatus string
		Battery        int64
		Load           int64
		Power          int64
		Runtime        string
	}

	var list []ups
	var totalLoad int64 = 0
	for _, client := range s.Clients {
		if client == nil {
			continue
		}
		upss, err := client.UPSs()
		if err != nil {
			log.Printf("[ERROR] get UPSs for %s: %v", client.Hostname, err)
			continue
		}
		if len(upss) == 0 {
			continue
		}
		for _, u := range upss {
			status, originalStatus, err := u.GetStatus()
			if err != nil {
				log.Printf("[ERROR] get status for %s: %v", u.Name, err)
				continue
			}
			battery, _, _, err := u.GetBattery()
			if err != nil {
				log.Printf("[ERROR] get battery for %s: %v", u.Name, err)
				continue
			}
			load, power, err := u.GetLoad()
			if err != nil {
				log.Printf("[ERROR] get load for %s: %v", u.Name, err)
				continue
			}
			runtime, err := u.GetRuntime()
			if err != nil {
				log.Printf("[ERROR] get runtime for %s: %v", u.Name, err)
				continue
			}
			formattedRuntime := time.Duration(runtime) * time.Second

			list = append(list, ups{
				ID:             u.ID,
				Name:           u.Name,
				Status:         status,
				OriginalStatus: originalStatus,
				Battery:        battery,
				Load:           load,
				Power:          power,
				Runtime:        formattedRuntime.String(),
			})
			totalLoad += power
		}
	}

	status := "unknown"
	for _, u := range list {
		if strings.Contains(u.OriginalStatus, "OL") {
			if status == "unknown" {
				status = "up"
			} else if status == "down" {
				status = "degraded"
			}
		} else if strings.Contains(u.OriginalStatus, "OB") {
			if status == "unknown" {
				status = "down"
			} else if status == "up" {
				status = "degraded"
			}
		}
	}

	data := struct {
		List      []ups
		Status    string
		TotalLoad int64
	}{
		List:      list,
		Status:    status,
		TotalLoad: totalLoad,
	}

	if err := s.Template.List.Execute(w, data); err != nil {
		log.Printf("[ERROR] generate list html: %v", err)
		http.Error(w, fmt.Sprintf("error generate list html: %v", err), http.StatusInternalServerError)
	}
}

func (s *Rest) details(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var ups *nut.UPS
	for _, c := range s.Clients {
		if u, err := c.UPS(id); err == nil && u != nil {
			ups = u
			break
		}
	}
	if ups == nil {
		s.notFound(w, r)
		return
	}

	type loadT struct {
		Value int64
		Power int64
	}
	type batteryT struct {
		Charge  int64
		Low     int64
		Voltage float64
	}
	type statusT struct {
		Value    string
		Original string
		Runtime  string
	}

	status, originalStatus, _ := ups.GetStatus()
	battery, low, voltage, _ := ups.GetBattery()
	load, power, _ := ups.GetLoad()
	runtime, _ := ups.GetRuntime()
	formattedRuntime := time.Duration(runtime) * time.Second

	data := struct {
		ID           string
		Name         string
		Description  string
		Manufacturer string
		Model        string
		Server       string
		Online       bool

		Load    loadT
		Battery batteryT
		Status  statusT

		Variables []nut.Variable
	}{
		ID:           ups.ID,
		Name:         ups.Name,
		Description:  ups.Description,
		Manufacturer: ups.Manufacturer,
		Model:        ups.Model,
		Server:       ups.Server,
		Online:       strings.Contains(originalStatus, "OL"),

		Load: loadT{
			Value: load,
			Power: power,
		},
		Battery: batteryT{
			Charge:  battery,
			Low:     low,
			Voltage: voltage,
		},
		Status: statusT{
			Value:    status,
			Original: originalStatus,
			Runtime:  formattedRuntime.String(),
		},

		Variables: ups.Variables,
	}

	if err := s.Template.Details.Execute(w, data); err != nil {
		log.Printf("[ERROR] generate details html: %v", err)
		http.Error(w, fmt.Sprintf("error generate details html: %v", err), http.StatusInternalServerError)
	}
}

func (s *Rest) static(w http.ResponseWriter, r *http.Request) {
	path := fmt.Sprintf("template%s", r.URL.Path)
	if _, err := s.Template.FS.Open(path); err != nil {
		s.notFound(w, r)
		return
	}
	http.ServeFileFS(w, r, s.Template.FS, path)
}
