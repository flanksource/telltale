package deploy_test

import (
	"bytes"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"sigs.k8s.io/yaml"

	"github.com/flanksource/telltale/pkg/deploy"
)

var (
	committedCRD = filepath.Join("..", "..", deploy.CRDFile)
	example      = filepath.Join("..", "..", "examples", "deployers", "orders.yaml")
)

func crdSchema() *apiextensions.JSONSchemaProps {
	data, err := os.ReadFile(committedCRD)
	Expect(err).ToNot(HaveOccurred())
	var crd apiextensionsv1.CustomResourceDefinition
	Expect(yaml.UnmarshalStrict(data, &crd)).To(Succeed())
	Expect(crd.Spec.Versions).To(HaveLen(1))
	var internal apiextensions.JSONSchemaProps
	Expect(apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &internal, nil)).To(Succeed())
	return &internal
}

var _ = Describe("the Deployer CRD", func() {
	It("is the one `task generate` writes from the types", func() {
		generated, err := deploy.CRD()
		Expect(err).ToNot(HaveOccurred())
		committed, err := os.ReadFile(committedCRD)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(committed)).To(Equal(string(generated)), "run `task generate`")
	})

	It("is a structural schema the API server accepts and keeps a javaContainer's own fields", func() {
		structural, err := structuralschema.NewStructural(crdSchema())
		Expect(err).ToNot(HaveOccurred())
		Expect(structuralschema.ValidateStructural(nil, structural)).To(BeEmpty())
		data, err := os.ReadFile(example)
		Expect(err).ToNot(HaveOccurred())
		for _, document := range bytes.Split(data, []byte("\n---\n")) {
			var object map[string]any
			Expect(yaml.Unmarshal(document, &object)).To(Succeed())
			validator, _, err := validation.NewSchemaValidator(crdSchema())
			Expect(err).ToNot(HaveOccurred())
			Expect(validation.ValidateCustomResource(nil, object, validator)).To(BeEmpty())
			pruned, err := yaml.Marshal(object)
			Expect(err).ToNot(HaveOccurred())
			pruning.Prune(object, structural, true)
			after, err := yaml.Marshal(object)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(after)).To(Equal(string(pruned)), "nothing is pruned")

			deployer, err := deploy.Decode(document)
			Expect(err).ToNot(HaveOccurred())
			_, err = deployer.Spec.JavaContainer.Container()
			Expect(err).ToNot(HaveOccurred(), "the example's %s javaContainer is valid", deployer.Spec.JavaContainer.Kind)
		}
	})
})
