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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	contentapi "github.com/containerd/containerd/api/services/content/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	typesapi "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/images"
	"github.com/containerd/log"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const uruncPrefix = "com.urunc.unikernel."

type annotationFetcher struct {
	containerLabels map[string]string
	contentClient   contentapi.ContentClient
	namespace       string
	target          *typesapi.Descriptor
	// dockerImageLabels holds urunc annotations recovered through the
	// Docker Engine API fallback (see fetchDockerImageLabels), used when
	// the container has no containerd-resolvable image reference.
	dockerImageLabels map[string]string
}

func newAnnotationFetcher(ctx context.Context, session *Session) (*annotationFetcher, error) {
	container := session.GetContainer()
	if container == nil {
		return nil, fmt.Errorf("container metadata is not loaded")
	}

	fetcher := &annotationFetcher{
		containerLabels: container.Labels,
		contentClient:   session.contentClient(),
		namespace:       session.GetNamespace(),
	}

	if container.Image == "" {
		// When Docker is not configured to use containerd's image/content
		// store (its default, classic storage), Docker still creates the
		// container through containerd, so containerLabels above is
		// populated as usual. However, the image itself was pulled and
		// stored by Docker directly, so there is no containerd image
		// record to resolve here, and container.Image is empty.
		//
		// containerd's image/content store is what lets us read the raw
		// OCI manifest and its Annotations further down. Classic Docker
		// storage does not retain that manifest wrapper at all, so we
		// cannot recover manifest annotations in this case. What Docker
		// does expose, through its own Engine API, is the image's OCI
		// config Labels (i.e. Dockerfile LABEL instructions), which is
		// the closest Docker-compatible equivalent. Fetch those as a
		// best-effort fallback.
		labels, err := fetchDockerImageLabels(ctx, session.GetContainerID())
		if err != nil {
			log.G(ctx).WithError(err).Debug("urunc(shim): docker annotation fallback unavailable")
			return fetcher, nil
		}
		fetcher.dockerImageLabels = labels
		return fetcher, nil
	}

	imageResp, err := session.imagesClient().Get(
		withNamespace(ctx, session.GetNamespace()),
		&imagesapi.GetImageRequest{Name: container.Image},
	)
	if err != nil {
		return fetcher, nil
	}

	fetcher.target = imageResp.GetImage().GetTarget()
	return fetcher, nil
}

func InjectUruncAnnotations(ctx context.Context, session *Session, bundlePath string) error {
	fetcher, err := newAnnotationFetcher(ctx, session)
	if err != nil {
		return fmt.Errorf("create annotation fetcher: %w", err)
	}
	annotations, err := fetcher.fetchUruncAnnotations(ctx)
	if err != nil {
		return fmt.Errorf("fetch urunc annotations: %w", err)
	}
	if len(annotations) == 0 {
		return nil
	}

	return patchConfigJSON(bundlePath, annotations)
}

func (f *annotationFetcher) fetchUruncAnnotations(ctx context.Context) (map[string]string, error) {
	filtered := make(map[string]string)

	// Collect urunc annotations from container labels.
	for k, v := range f.containerLabels {
		if strings.HasPrefix(k, uruncPrefix) {
			filtered[k] = v
		}
	}

	// Collect urunc annotations recovered through the Docker Engine API
	// fallback (only populated when there was no containerd image to
	// resolve a manifest from, see newAnnotationFetcher). These take
	// precedence over container labels, the same way manifest annotations
	// do below, since they represent the same image-level metadata.
	for k, v := range f.dockerImageLabels {
		if strings.HasPrefix(k, uruncPrefix) {
			filtered[k] = v
		}
	}

	// Collect urunc annotations from manifest
	if f.target == nil || !images.IsManifestType(f.target.MediaType) {
		// If the image target is missing or does not point to a manifest,
		// keep the labels collected so far.
		return filtered, nil
	}

	manifestRaw, err := readBlob(ctx, f.namespace, f.contentClient, f.target.Digest, f.target.Size)
	if err != nil {
		return nil, fmt.Errorf("read manifest blob: %w", err)
	}

	var manifest imagespec.Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}

	// Manifest annotations override config labels on duplicate keys.
	for k, v := range manifest.Annotations {
		if strings.HasPrefix(k, uruncPrefix) {
			filtered[k] = v
		}
	}

	return filtered, nil
}

// readBlob reads a blob with the given digest from containerd's content store
// and returns it as a byte slice.
func readBlob(ctx context.Context, namespace string, contentClient contentapi.ContentClient, digest string, size int64) ([]byte, error) {
	stream, err := contentClient.Read(withNamespace(ctx, namespace), &contentapi.ReadContentRequest{
		Digest: digest,
		Size:   size,
	})
	if err != nil {
		return nil, containerdErr(err)
	}

	var raw []byte
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, containerdErr(err)
		}
		raw = append(raw, resp.Data...)
	}

	return raw, nil
}

// patchConfigJSON injects missing annotations into the OCI runtime spec
// stored in the bundle's config.json.
//
// Existing annotations in config.json are preserved. Only annotation keys that
// are not already present in the runtime spec are added.
func patchConfigJSON(bundlePath string, annotations map[string]string) error {
	configPath := filepath.Join(bundlePath, "config.json")

	fi, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("stat config.json: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config.json: %w", err)
	}

	var spec runtimespec.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("unmarshal spec: %w", err)
	}

	if spec.Annotations == nil {
		spec.Annotations = make(map[string]string)
	}

	for k, v := range annotations {
		if _, exists := spec.Annotations[k]; exists {
			continue
		}
		spec.Annotations[k] = v
	}

	patched, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}

	if err := atomicWriteFile(configPath, patched, fi.Mode()); err != nil {
		return fmt.Errorf("write config.json atomically: %w", err)
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tmpDir := filepath.Dir(path)

	f, err := os.CreateTemp(tmpDir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}

	tmpName := f.Name()
	defer os.Remove(tmpName)

	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	return nil
}
