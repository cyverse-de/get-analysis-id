package main

import (
	"context"
	"encoding/json"
	"errors"
	_ "expvar"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/cyverse-de/go-mod/otelutils"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var httpClient = http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

const serviceName = "get-analysis-id"

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
func GetAnalysisID(ctx context.Context, appsURL, appsUser, externalID string) (*Analysis, error) {
	reqURL, err := url.Parse(appsURL)
	if err != nil {
		return nil, err
	}
	reqURL.Path = filepath.Join(reqURL.Path, "admin/analyses/by-external-id", externalID)

	v := url.Values{}
	v.Set("user", appsUser)
	reqURL.RawQuery = v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()
	if err != nil {
		return nil, err
	}

	analyses := &Analyses{}
	b, err := io.ReadAll(resp.Body)
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

	var tracerCtx, cancel = context.WithCancel(context.Background())
	defer cancel()
	shutdown := otelutils.TracerProviderFromEnv(tracerCtx, serviceName, func(e error) { log.Fatal(e) })
	defer shutdown()

	handler := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var rb []byte
		rb, err = io.ReadAll(r.Body)
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
		analysis, err = GetAnalysisID(ctx, *appsURL, *appsUser, idReq.ExternalID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(analysis) // nolint:errcheck
	}), "/")

	http.Handle("/", handler)

	addr := fmt.Sprintf(":%d", *listenPort)
	if useSSL {
		log.Fatal(http.ListenAndServeTLS(addr, *sslCert, *sslKey, nil))
	} else {
		log.Fatal(http.ListenAndServe(addr, nil))
	}
}
