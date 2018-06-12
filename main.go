package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
)

// Analysis contains the ID for the Analysis, which gets used as the resource
// name when checking permissions.
type Analysis struct {
	ID string `json:"id"` // Literally all we care about here.
}

// Analyses is a list of analyses returned by the apps service.
type Analyses struct {
	Analyses []Analysis `json:"analyses"`
}

// GetAnalysisID returns the Analysis ID returned for the given external ID.
func GetAnalysisID(appsURL, appsUser, externalID string) (*Analysis, error) {
	reqURL, err := url.Parse(appsURL)
	if err != nil {
		return nil, err
	}
	reqURL.Path = filepath.Join(reqURL.Path, "admin/analyses/by-external-id", externalID)

	v := url.Values{}
	v.Set("user", appsUser)
	reqURL.RawQuery = v.Encode()

	resp, err := http.Get(reqURL.String())
	defer func() {
		if resp != nil {
			if resp.Body != nil {
				resp.Body.Close()
			}
		}
	}()
	if err != nil {
		return nil, err
	}

	analyses := &Analyses{}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, analyses); err != nil {
		return nil, err
	}
	if len(analyses.Analyses) < 1 {
		return nil, errors.New("no analyses found")
	}
	return &analyses.Analyses[0], nil
}

// IDRequest is the format that incoming requests should be in.
type IDRequest struct {
	ExternalID string `json:"external_id"`
}

func main() {
	var (
		err        error
		appsUser   = flag.String("apps-user", "", "Username to use when calling the apps api.")
		appsURL    = flag.String("apps-url", "http://apps", "The URL for the apps service.")
		listenPort = flag.Int("listen-port", 60000, "The port to listen on.")
		sslCert    = flag.String("ssl-cert", "", "Path to the SSL .crt file.")
		sslKey     = flag.String("ssl-key", "", "Path to the SSL .key file.")
	)

	flag.Parse()

	if *appsUser == "" {
		log.Fatal("--apps-user must be set.")
	}

	useSSL := false
	if *sslCert != "" || *sslKey != "" {
		if *sslCert == "" {
			log.Fatal("--ssl-cert is required with --ssl-key.")
		}

		if *sslKey == "" {
			log.Fatal("--ssl-key is required with --ssl-cert.")
		}
		useSSL = true
	}

	r := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rb []byte
		rb, err = ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		idReq := &IDRequest{}

		if err = json.Unmarshal(rb, idReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if idReq.ExternalID == "" {
			http.Error(w, "external ID must be set", http.StatusBadRequest)
			return
		}

		var analysis *Analysis
		analysis, err = GetAnalysisID(*appsURL, *appsUser, idReq.ExternalID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(analysis)
	})

	server := &http.Server{
		Handler: r,
		Addr:    fmt.Sprintf(":%d", *listenPort),
	}
	if useSSL {
		err = server.ListenAndServeTLS(*sslCert, *sslKey)
	} else {
		err = server.ListenAndServe()
	}
	log.Fatal(err)
}
