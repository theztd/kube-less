package manifest

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// Parser provides functionality to parse Kubernetes YAML manifests.
type Parser struct {
	decoder runtime.Decoder
	// Add a mapping for supported GVKs if needed, or check types directly
}

// NewParser creates a new Parser instance.
func NewParser() *Parser {
	return &Parser{
		decoder: scheme.Codecs.UniversalDeserializer(),
	}
}

// Parse takes a byte slice containing one or more YAML documents and
// returns a slice of parsed Kubernetes objects.
// It only decodes objects that are part of the k8s.io/client-go scheme.Scheme
// and specifically filters for Deployment, ConfigMap, and Secret.
func (p *Parser) Parse(data []byte) ([]runtime.Object, error) {
	var objects []runtime.Object
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to decode YAML object: %w", err)
		}

		if len(rawObj.Raw) == 0 {
			continue // Skip empty documents
		}

		obj, gvk, err := p.decoder.Decode(rawObj.Raw, nil, nil)
		if err != nil {
			// If it's an unrecognized type, we might want to log and skip, or return error.
			// For now, let's log and skip.
			log.Printf("Warning: Unrecognized object type in manifest, skipping: %v, error: %v", string(rawObj.Raw), err)
			continue
		}

		// Filter for supported types
		if p.isSupportedGVK(*gvk) {
			objects = append(objects, obj)
		} else {
			log.Printf("Info: Skipping unsupported GVK: %s/%s/%s", gvk.Group, gvk.Version, gvk.Kind)
		}
	}

	return objects, nil
}

// isSupportedGVK checks if the given GroupVersionKind is one of the types we care about.
func (p *Parser) isSupportedGVK(gvk schema.GroupVersionKind) bool {
	switch gvk {
	case appsv1.SchemeGroupVersion.WithKind("Deployment"):
		return true
	case corev1.SchemeGroupVersion.WithKind("ConfigMap"):
		return true
	case corev1.SchemeGroupVersion.WithKind("Secret"):
		return true
	default:
		return false
	}
}

// LoadAndParseFile reads a file from the given path and parses its contents.
// This is a convenience function for direct file parsing, not used by the watcher directly.
func (p *Parser) LoadAndParseFile(filePath string) ([]runtime.Object, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	return p.Parse(data)
}
