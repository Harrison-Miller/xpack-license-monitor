package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

func JSONResponse(w http.ResponseWriter, i interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(i)
}

func JSONError(w http.ResponseWriter, errorMessage string) {
	JSONResponse(w, struct {
		Error string `json:"error"`
	}{
		Error: errorMessage,
	})
}

type Config struct {
	Domain      string `json:"domain"`
	ClusterPath string `json:"clusterPath"`
	LicensePath string `json:"licensePath`
}

type Env struct {
	Config   Config
	logger   *log.Logger
	Clusters []*Cluster
	License  ClusterLicense
}

func (e *Env) save() error {
	file, err := os.OpenFile(e.Config.ClusterPath, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		return err
	}
	defer file.Close()

	err = json.NewEncoder(file).Encode(&e.Clusters)
	return err
}

func (e *Env) loadLicense() error {
	file, err := os.Open(e.Config.LicensePath)
	if err != nil {
		return err
	}

	var license ClusterLicense
	err = json.NewDecoder(file).Decode(&struct {
		*ClusterLicense `json:"license"`
	}{
		&license,
	})

	if err != nil {
		return err
	}
	license.ExpiryTime = time.Unix(0, license.Expiry*int64(time.Millisecond))
	e.License = license

	return nil
}

func (e *Env) load() error {
	e.loadLicense()

	file, err := os.Open(e.Config.ClusterPath)
	if err != nil {
		return err
	}
	defer file.Close()

	err = json.NewDecoder(file).Decode(&e.Clusters)
	if err != nil {
		return err
	}

	for _, c := range e.Clusters {
		err := c.Monitor(e.Config.LicensePath)
		if err != nil {
			e.logger.Println(err)
		}
		stopchan := make(chan struct{})
		c.stopchan = stopchan
		go e.monitor(c)
	}

	return nil
}

func (e *Env) envHandler(handler func(e *Env, w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	h := func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler(e, w, r)
		e.logger.Printf("%s - %s %v %s %dms\n", r.RemoteAddr, r.Method, r.URL, r.Proto, time.Since(start)/time.Millisecond)
	}
	return h
}

func (e *Env) hasCluster(name string) bool {
	for _, h := range e.Clusters {
		if h.Hostname == name || h.Status.ClusterName == name {
			return true
		}
	}
	return false
}

func index(e *Env, w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseFiles("templates/index.html")
	if err != nil {
		http.Error(w, "unable to load index.html template", http.StatusInternalServerError)
		return
	}

	t.Execute(w, e)
}

func add(e *Env, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		e.logger.Println(err)
		http.Error(w, "error parsing form inputs", http.StatusBadRequest)
		return
	}

	if hostnameParams, ok := r.Form["hostname"]; ok {
		hostname := hostnameParams[0]

		if !strings.HasSuffix(hostname, e.Config.Domain) {
			JSONError(w, fmt.Sprintf("%s does not end in %s", hostname, e.Config.Domain))
			return
		}

		if e.hasCluster(hostname) {
			JSONError(w, fmt.Sprintf("%s is already being monitored", hostname))
			return
		}

		useSSL, err := strconv.ParseBool(r.FormValue("usessl"))
		if err != nil {
			e.logger.Println(err)
			useSSL = true
		}

		username := "elastic"
		if val := r.FormValue("username"); val != "" {
			username = val
		}

		password := "changeme"
		if val := r.FormValue("password"); password != "" {
			password = val
		}

		cluster := &Cluster{
			Hostname: hostname,
			UseSSL:   useSSL,
			Username: username,
			Password: password,
		}

		err = cluster.Monitor(e.Config.LicensePath)
		if err != nil {
			e.logger.Println(err)
			JSONError(w, "error getting cluster status")
			return
		}

		if e.hasCluster(cluster.Status.ClusterName) {
			JSONError(w, fmt.Sprintf("%s is already being monitored", cluster.Status.ClusterName))
			return
		}

		e.Clusters = append(e.Clusters, cluster)
		err = e.save()
		if err != nil {
			e.logger.Println(err)
		}
		stopchan := make(chan struct{})
		cluster.stopchan = stopchan
		go e.monitor(cluster)

		JSONResponse(w, struct {
			Message  string `json:"message"`
			Hostname string `json:"hostname"`
		}{
			Message:  "success",
			Hostname: hostname,
		})
		return
	}

	http.Error(w, "missing hostname form input", http.StatusBadRequest)
}

func refresh(e *Env, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		e.logger.Println(err)
		http.Error(w, "error parsing form inputs", http.StatusBadRequest)
		return
	}

	if clusterParams, ok := r.Form["cluster"]; ok {
		clusterName := clusterParams[0]

		found := false
		for _, c := range e.Clusters {
			if c.Status.ClusterName == clusterName {
				err := c.Monitor(e.Config.LicensePath)
				if err != nil {
					e.logger.Println(err)
					http.Error(w, "error refreshing cluster", http.StatusInternalServerError)
				}
				found = true
				break
			}
		}

		if !found {
			http.Error(w, "no such cluster", http.StatusBadRequest)
		}

		JSONResponse(w, struct {
			Message string `json:"message"`
			Cluster string `json:"cluster"`
		}{
			Message: "success",
			Cluster: clusterName,
		})
		return
	}

	http.Error(w, "missing cluster name form input", http.StatusBadRequest)
}

func remove(e *Env, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		e.logger.Println(err)
		http.Error(w, "error parsing form inputs", http.StatusBadRequest)
		return
	}

	if clusterParams, ok := r.Form["cluster"]; ok {
		clusterName := clusterParams[0]

		found := false
		for i, c := range e.Clusters {
			if c.Status.ClusterName == clusterName {
				close(c.stopchan)
				e.Clusters = append(e.Clusters[:i], e.Clusters[i+1:]...)
				err := e.save()
				if err != nil {
					e.logger.Println(err)
					http.Error(w, "error saving cluster list", http.StatusInternalServerError)
				}
				found = true
				break
			}
		}

		if !found {
			http.Error(w, "no such cluster", http.StatusBadRequest)
		}

		JSONResponse(w, struct {
			Message string `json:"message"`
			Cluster string `json:"cluster"`
		}{
			Message: "success",
			Cluster: clusterName,
		})
		return
	}

	http.Error(w, "missing cluster name form input", http.StatusBadRequest)
}

func setLicense(e *Env, w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		e.logger.Println(err)
		http.Error(w, "error parsing form inputs", http.StatusBadRequest)
		return
	}

	if clusterParams, ok := r.Form["cluster"]; ok {
		clusterName := clusterParams[0]

		found := false
		for _, c := range e.Clusters {
			if c.Status.ClusterName == clusterName {
				err := c.UpdateLicense(e.Config.LicensePath)
				if err != nil {
					e.logger.Println(err)
					http.Error(w, "error setting license", http.StatusInternalServerError)
				}
				found = true
				break
			}
		}

		if !found {
			http.Error(w, "no such cluster", http.StatusBadRequest)
		}

		JSONResponse(w, struct {
			Message string `json:"message"`
			Cluster string `json:"cluster"`
		}{
			Message: "success",
			Cluster: clusterName,
		})
		return
	}

	http.Error(w, "missing cluster name form input", http.StatusBadRequest)
}

func (e *Env) monitor(cluster *Cluster) {
	name := fmt.Sprintf("[%s] ", cluster.Status.ClusterName)
	logger := log.New(os.Stdout, name, log.LstdFlags)
	logger.Printf("Monitoring %s through %s\n", cluster.Status.ClusterName, cluster.Hostname)

	for {
		logger.Println("Waiting 1 hour before next check")
		time.Sleep(1 * time.Hour)

		select {
		default:
			break
		case <-cluster.stopchan:
			logger.Println("Shutting down monitor for ", cluster.Status.ClusterName)
			return
		}

		logger.Println("Checking ", cluster.Status.ClusterName)

		err := cluster.Monitor(e.Config.LicensePath)
		if err != nil {
			logger.Println(err)
		}
	}
}

func main() {
	logger := log.New(os.Stdout, "[main] ", log.LstdFlags)
	logger.Println("XPack License Monitor")

	domain := "example.com"
	if value, ok := os.LookupEnv("DOMAIN"); ok {
		domain = value
	}

	config := Config{
		Domain:      domain,
		ClusterPath: "config/clusters.json",
		LicensePath: "license.json",
	}

	env := Env{
		Config: config,
		logger: logger,
	}

	err := env.load()
	if err != nil {
		logger.Println(err)
	}

	r := mux.NewRouter()

	r.HandleFunc("/", env.envHandler(index)).Methods("GET")
	r.HandleFunc("/add", env.envHandler(add)).Methods("GET")
	r.HandleFunc("/refresh", env.envHandler(refresh)).Methods("GET")
	r.HandleFunc("/remove", env.envHandler(remove)).Methods("GET")
	r.HandleFunc("/set", env.envHandler(setLicense)).Methods("GET")

	err = http.ListenAndServe(":8080", r)
	if err != nil {
		logger.Fatal(err)
	}

}
