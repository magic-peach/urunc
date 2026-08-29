// Copyright (c) 2023-2026, Nubificus LTD
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package containerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// defaultDockerSocket is the well-known path of the Docker Engine API
// socket on a plain Docker host. It is only used when DOCKER_HOST does not
// point us somewhere else.
const defaultDockerSocket = "/var/run/docker.sock"

// dockerAPITimeout bounds each request to the Docker Engine API. Container
// creation should not stall indefinitely because a Docker daemon is slow
// or unreachable.
const dockerAPITimeout = 5 * time.Second

// dockerContainerInspect is the small subset of Docker's container inspect
// response (GET /containers/{id}/json) that we actually need: the ID of the
// image the container was created from.
type dockerContainerInspect struct {
	Image string `json:"Image"`
}

// dockerImageInspect is the small subset of Docker's image inspect response
// (GET /images/{id}/json) that we actually need: the image's OCI config
// Labels, i.e. the labels baked in via Dockerfile LABEL instructions.
type dockerImageInspect struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// dockerSocketPath resolves the Docker Engine API socket to use, honoring
// DOCKER_HOST when it is set to a unix socket, and falling back to the
// well-known default path otherwise. A non-unix DOCKER_HOST (e.g. tcp://,
// ssh://) is not something we can talk to with a plain unix dial, so in
// that case we also fall back to the default path rather than guessing.
func dockerSocketPath() string {
	host := os.Getenv("DOCKER_HOST")
	if strings.HasPrefix(host, "unix://") {
		return strings.TrimPrefix(host, "unix://")
	}
	return defaultDockerSocket
}

// newDockerHTTPClient returns an HTTP client that talks to the Docker
// Engine API over the given unix socket.
func newDockerHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: dockerAPITimeout,
	}
}

// dockerAPIGet performs a GET request against the Docker Engine API over
// the given client and decodes a JSON response into out. It returns
// (false, nil) without an error when the Docker daemon responds with 404,
// so callers can tell "not found" apart from a real failure.
func dockerAPIGet(ctx context.Context, client *http.Client, path string, out interface{}) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return false, fmt.Errorf("build request for %s: %w", path, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("request %s: unexpected status %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return false, fmt.Errorf("decode response from %s: %w", path, err)
	}
	return true, nil
}

// fetchDockerImageLabels recovers the OCI config Labels of the image a
// container was created from, using the Docker Engine API directly. This
// is meant as a fallback for containers created by a plain Docker daemon
// that is not configured to use containerd's image/content store: in that
// setup, containerd knows about the container itself (Docker still creates
// it through containerd), but not which image it came from, since Docker
// pulled and stored that image itself.
//
// The Docker Engine API only ever gives us the image's config Labels, not
// raw OCI manifest Annotations: classic (non-containerd-backed) Docker
// image storage does not retain the manifest wrapper that annotations live
// in. So, unlike the containerd/manifest path, urunc annotations set as
// manifest annotations rather than image labels cannot be recovered this
// way.
//
// It returns (nil, nil), without an error, when there is no Docker socket
// to talk to (e.g. a plain containerd/Kubernetes host), since that is the
// expected case, not a failure.
func fetchDockerImageLabels(ctx context.Context, containerID string) (map[string]string, error) {
	socketPath := dockerSocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat docker socket %s: %w", socketPath, err)
	}

	client := newDockerHTTPClient(socketPath)

	var container dockerContainerInspect
	found, err := dockerAPIGet(ctx, client, "/containers/"+url.PathEscape(containerID)+"/json", &container)
	if err != nil {
		return nil, fmt.Errorf("inspect docker container: %w", err)
	}
	if !found || container.Image == "" {
		return nil, nil
	}

	var image dockerImageInspect
	found, err = dockerAPIGet(ctx, client, "/images/"+url.PathEscape(container.Image)+"/json", &image)
	if err != nil {
		return nil, fmt.Errorf("inspect docker image: %w", err)
	}
	if !found {
		return nil, nil
	}

	return image.Config.Labels, nil
}
