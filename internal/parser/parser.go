package parser

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
}

// NewParser creates a new Parser instance.
func NewParser() *Parser {
	return &Parser{
		decoder: scheme.Codecs.UniversalDeserializer(),
	}
}

// Parse takes a byte slice containing one or more YAML documents and
// returns a slice of parsed Kubernetes objects.
// Supported types: Deployment, ConfigMap, Secret.
// Unknown types are logged and skipped without error.
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
			continue
		}

		obj, gvk, err := p.decoder.Decode(rawObj.Raw, nil, nil)
		if err != nil {
			log.Printf("Warning: unrecognized object in manifest, skipping: %v", err)
			continue
		}

		if p.isSupportedGVK(*gvk) {
			objects = append(objects, obj)
		} else {
			log.Printf("Info: skipping unsupported GVK: %s/%s/%s", gvk.Group, gvk.Version, gvk.Kind)
		}
	}

	return objects, nil
}

// isSupportedGVK checks if the given GroupVersionKind is one of the types we handle.
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
func (p *Parser) LoadAndParseFile(filePath string) ([]runtime.Object, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	return p.Parse(data)
}
