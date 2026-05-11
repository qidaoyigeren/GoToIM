package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Instance mirrors the naming.Instance struct for JSON compatibility.
type Instance struct {
	Region   string            `json:"region"`
	Zone     string            `json:"zone"`
	Env      string            `json:"env"`
	AppID    string            `json:"appid"`
	Hostname string            `json:"hostname"`
	Addrs    []string          `json:"addrs"`
	Version  string            `json:"version"`
	LastTs   int64             `json:"latest_timestamp"`
	Metadata map[string]string `json:"metadata"`
}

// InstancesInfo mirrors naming.InstancesInfo.
type InstancesInfo struct {
	Instances map[string][]*Instance `json:"instances"`
	LastTs    int64                  `json:"latest_timestamp"`
}

// store holds all registered instances keyed by "appid:env".
var (
	store   sync.Map
	tsSeq   int64
)

func nextTs() int64 {
	return atomic.AddInt64(&tsSeq, 1)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/discovery/register", handleRegister)
	mux.HandleFunc("/discovery/renew", handleRenew)
	mux.HandleFunc("/discovery/cancel", handleCancel)
	mux.HandleFunc("/discovery/set", handleSet)
	mux.HandleFunc("/discovery/polls", handlePolls)
	mux.HandleFunc("/discovery/fetch", handleFetch)

	log.Println("discovery: listening on :7171")
	if err := http.ListenAndServe(":7171", mux); err != nil {
		log.Fatalf("discovery: %v", err)
	}
}

func sendOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "message": "ok"})
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"code": code, "message": msg})
}

func storeKey(appid, env string) string {
	return appid + ":" + env
}

// handleRegister registers a new instance.
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, -400, "method not allowed")
		return
	}
	r.ParseForm()
	appid := r.FormValue("appid")
	env := r.FormValue("env")
	zone := r.FormValue("zone")
	region := r.FormValue("region")
	hostname := r.FormValue("hostname")
	addrs := strings.Split(r.FormValue("addrs"), ",")
	version := r.FormValue("version")
	metadataStr := r.FormValue("metadata")

	metadata := map[string]string{}
	if metadataStr != "" {
		json.Unmarshal([]byte(metadataStr), &metadata)
	}

	ts := nextTs()
	ins := &Instance{
		Region:   region,
		Zone:     zone,
		Env:      env,
		AppID:    appid,
		Hostname: hostname,
		Addrs:    addrs,
		Version:  version,
		LastTs:   ts,
		Metadata: metadata,
	}

	key := storeKey(appid, env)
	val, _ := store.LoadOrStore(key, &appData{
		instances: map[string][]*Instance{},
		lastTs:    0,
	})
	ad := val.(*appData)
	ad.mu.Lock()
	ad.instances[zone] = append(ad.instances[zone], ins)
	ad.lastTs = ts
	ad.mu.Unlock()

	log.Printf("discovery: register appid=%s env=%s zone=%s hostname=%s addrs=%v", appid, env, zone, hostname, addrs)
	sendOK(w)
}

// handleRenew renews an instance heartbeat.
func handleRenew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, -400, "method not allowed")
		return
	}
	r.ParseForm()
	appid := r.FormValue("appid")
	env := r.FormValue("env")

	key := storeKey(appid, env)
	val, ok := store.Load(key)
	if !ok {
		fail(w, -404, "instance not found")
		return
	}
	ad := val.(*appData)
	ad.mu.Lock()
	ad.lastTs = nextTs()
	ad.mu.Unlock()

	log.Printf("discovery: renew appid=%s env=%s", appid, env)
	sendOK(w)
}

// handleCancel removes an instance.
func handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, -400, "method not allowed")
		return
	}
	r.ParseForm()
	appid := r.FormValue("appid")
	env := r.FormValue("env")
	hostname := r.FormValue("hostname")

	key := storeKey(appid, env)
	val, loaded := store.Load(key)
	if !loaded {
		sendOK(w)
		return
	}
	ad := val.(*appData)
	ad.mu.Lock()
	for zone, instances := range ad.instances {
		filtered := make([]*Instance, 0, len(instances))
		for _, ins := range instances {
			if ins.Hostname != hostname {
				filtered = append(filtered, ins)
			}
		}
		if len(filtered) == 0 {
			delete(ad.instances, zone)
		} else {
			ad.instances[zone] = filtered
		}
	}
	ad.lastTs = nextTs()
	ad.mu.Unlock()

	log.Printf("discovery: cancel appid=%s env=%s hostname=%s", appid, env, hostname)
	sendOK(w)
}

// handleSet updates instance metadata.
func handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, -400, "method not allowed")
		return
	}
	r.ParseForm()
	appid := r.FormValue("appid")
	env := r.FormValue("env")
	metadataStr := r.FormValue("metadata")

	key := storeKey(appid, env)
	val, exists := store.Load(key)
	if !exists {
		fail(w, -404, "instance not found")
		return
	}
	ad := val.(*appData)
	if metadataStr != "" {
		metadata := map[string]string{}
		json.Unmarshal([]byte(metadataStr), &metadata)
		ad.mu.Lock()
		for _, instances := range ad.instances {
			for _, ins := range instances {
				for k, v := range metadata {
					ins.Metadata[k] = v
				}
				ins.LastTs = nextTs()
			}
		}
		ad.lastTs = nextTs()
		ad.mu.Unlock()
	}

	log.Printf("discovery: set appid=%s env=%s", appid, env)
	sendOK(w)
}

// handlePolls returns changed instances (simplified: always returns current data).
func handlePolls(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	env := r.FormValue("env")
	appids := r.Form["appid"]
	lastTss := r.Form["latest_timestamp"]

	if len(appids) == 0 {
		fail(w, -400, "appid required")
		return
	}

	result := map[string]*InstancesInfo{}
	for i, appid := range appids {
		key := storeKey(appid, env)
		val, ok := store.Load(key)
		if !ok {
			continue
		}
		ad := val.(*appData)
		ad.mu.RLock()
		// Check if data has changed
		var clientTs int64
		if i < len(lastTss) {
			clientTs, _ = strconv.ParseInt(lastTss[i], 10, 64)
		}
		if ad.lastTs <= clientTs {
			ad.mu.RUnlock()
			continue
		}
		// Deep copy instances
		instances := map[string][]*Instance{}
		for zone, ins := range ad.instances {
			copied := make([]*Instance, len(ins))
			for j, in := range ins {
				ci := *in
				copied[j] = &ci
			}
			instances[zone] = copied
		}
		ts := ad.lastTs
		ad.mu.RUnlock()

		result[appid] = &InstancesInfo{
			Instances: instances,
			LastTs:    ts,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if len(result) == 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{"code": -304, "message": "not modified"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": result})
}

// handleFetch returns current instances.
func handleFetch(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	env := r.FormValue("env")
	appid := r.FormValue("appid")

	if appid == "" {
		fail(w, -400, "appid required")
		return
	}

	key := storeKey(appid, env)
	val, ok := store.Load(key)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"code":    0,
			"data":    map[string]*InstancesInfo{},
		})
		return
	}

	ad := val.(*appData)
	ad.mu.RLock()
	instances := map[string][]*Instance{}
	for zone, ins := range ad.instances {
		copied := make([]*Instance, len(ins))
		for j, in := range ins {
			ci := *in
			copied[j] = &ci
		}
		instances[zone] = copied
	}
	ts := ad.lastTs
	ad.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code": 0,
		"data": map[string]*InstancesInfo{
			appid: {
				Instances: instances,
				LastTs:    ts,
			},
		},
	})
}

type appData struct {
	mu        sync.RWMutex
	instances map[string][]*Instance
	lastTs    int64
}

// Register discovery itself so clients can find discovery nodes.
func init() {
	ts := nextTs()
	key := storeKey("infra.discovery", "dev")
	store.Store(key, &appData{
		instances: map[string][]*Instance{
			"sh001": {
				{
					Region:   "sh",
					Zone:     "sh001",
					Env:      "dev",
					AppID:    "infra.discovery",
					Hostname: "discovery",
					Addrs:    []string{"http://discovery:7171"},
					Version:  "1.0.0",
					LastTs:   ts,
					Metadata: map[string]string{},
				},
			},
		},
		lastTs: ts,
	})
	_ = time.Now() // ensure time import is used
}
