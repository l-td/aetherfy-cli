package vectors

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// DefaultEndpoint is the global vectors endpoint, the SDKs' DEFAULT_ENDPOINT
// (aetherfy_vectors/client.py, src/client.ts). It is where region discovery is
// asked, and where requests go when nothing else names an endpoint.
const DefaultEndpoint = "https://vectors.aetherfy.com"

// ValidRegions are the API regions --api-region accepts, the SDKs'
// VALID_REGIONS.
var ValidRegions = []string{"us-east-1", "eu-central-1", "ap-southeast-1"}

// Environment variables, the SDKs' names.
const (
	EnvVectorsURL = "AETHERFY_VECTORS_URL"
	EnvAPIRegion  = "AETHERFY_VECTORS_API_REGION"
	EnvWorkspace  = "AETHERFY_WORKSPACE"
)

// Where an endpoint came from, as the --json output names it.
const (
	SourceFlag     = "flag"
	SourceEnv      = "env"
	SourceRegion   = "region"
	SourceDefault  = "default"
	discoveryRoute = "regions"
)

// InvalidRegionError is an API region outside ValidRegions, refused before any
// request.
type InvalidRegionError struct{ Region string }

func (e *InvalidRegionError) Error() string {
	return fmt.Sprintf("api_region must be one of %s, got '%s'", strings.Join(ValidRegions, ", "), e.Region)
}

// Options are what the user gave on the command line.
type Options struct {
	// Endpoint is --vectors-url, the SDKs' endpoint= argument.
	Endpoint string
	// APIRegion is --api-region, the SDKs' api_region= argument.
	APIRegion string
	// Workspace is --workspace. WorkspaceSet says it was passed at all, so
	// `--workspace ""` forces no workspace the way the SDKs' workspace=None
	// does, even where AETHERFY_WORKSPACE is set.
	Workspace    string
	WorkspaceSet bool
}

// Resolved is where a command's requests go.
type Resolved struct {
	Endpoint       string
	EndpointSource string
	Workspace      string
	// Warning is set when an env URL overrode an API region, which the SDKs
	// log rather than refuse.
	Warning string
}

// Resolver carries the lookups resolution needs, so tests can supply them.
type Resolver struct {
	Getenv func(string) string
	// DiscoveryBase is where GET /api/v1/regions is asked. DefaultEndpoint.
	DiscoveryBase string
	APIKey        string
	send          sender
}

// NewResolver resolves against the real environment and the real discovery
// endpoint.
func NewResolver(getenv func(string) string, apiKey string) *Resolver {
	return &Resolver{Getenv: getenv, DiscoveryBase: DefaultEndpoint, APIKey: apiKey, send: httpSend}
}

// Resolve picks the endpoint and the workspace in the SDKs' order
// (AetherfyVectorsClient.__init__):
//
//  1. --vectors-url, the explicit endpoint;
//  2. AETHERFY_VECTORS_URL. It wins over an API region too, with a warning:
//     the control plane injects it on every agent machine;
//  3. --api-region, else AETHERFY_VECTORS_API_REGION, resolved to a URL by
//     GET /api/v1/regions on the default endpoint;
//  4. the default endpoint.
//
// An API region outside ValidRegions is refused before any request, as the
// SDKs refuse it at construction.
//
// The workspace is --workspace when passed, else AETHERFY_WORKSPACE, else none:
// the Python SDK's workspace="auto". The control plane sets that variable only
// on an agent that has a workspace.
func (r *Resolver) Resolve(o Options) (*Resolved, error) {
	region := o.APIRegion
	if region == "" {
		region = r.Getenv(EnvAPIRegion)
	}
	if region != "" && !contains(ValidRegions, region) {
		return nil, &InvalidRegionError{Region: region}
	}

	out := &Resolved{}
	envURL := r.Getenv(EnvVectorsURL)
	switch {
	case o.Endpoint != "":
		out.Endpoint, out.EndpointSource = o.Endpoint, SourceFlag
	case envURL != "":
		out.Endpoint, out.EndpointSource = envURL, SourceEnv
		if region != "" {
			out.Warning = fmt.Sprintf("Both %s and api_region=%s are set; using %s — "+
				"api_region= is a standalone/local-dev override, the injected URL wins "+
				"in integrated agents", EnvVectorsURL, region, EnvVectorsURL)
		}
	case region != "":
		u, err := r.discover(region)
		if err != nil {
			return nil, err
		}
		out.Endpoint, out.EndpointSource = u, SourceRegion
	default:
		out.Endpoint, out.EndpointSource = DefaultEndpoint, SourceDefault
	}
	out.Endpoint = strings.TrimRight(out.Endpoint, "/")

	if o.WorkspaceSet {
		out.Workspace = o.Workspace
	} else {
		out.Workspace = r.Getenv(EnvWorkspace)
	}
	return out, nil
}

// discover is the SDKs' _resolve_region_endpoint, with its messages.
func (r *Resolver) discover(region string) (string, error) {
	target := apiURL(r.DiscoveryBase, discoveryRoute)
	c := &Client{apiKey: r.APIKey}
	status, body, err := r.send("GET", target, nil, c.headers(false), DefaultTimeout)
	if err != nil {
		return "", fmt.Errorf("Could not resolve region '%s' via discovery: %v. "+
			"Check that the default endpoint is reachable, or pass --vectors-url directly.", region, err)
	}
	if status != 200 {
		return "", fmt.Errorf("Region discovery returned %d from %s. "+
			"Check that your API key is valid for the discovery endpoint.", status, target)
	}
	regions := map[string]string{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &regions); err != nil {
			return "", fmt.Errorf("Region discovery returned non-JSON body: %v", err)
		}
	}
	u, ok := regions[region]
	if !ok || u == "" {
		known := make([]string, 0, len(regions))
		for k := range regions {
			known = append(known, k)
		}
		sort.Strings(known)
		return "", fmt.Errorf("Region '%s' not configured at the discovery endpoint (available: [%s]).",
			region, strings.Join(known, ", "))
	}
	return u, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
