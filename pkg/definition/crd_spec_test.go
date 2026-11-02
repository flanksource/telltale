package definition_test

import (
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

	"github.com/flanksource/telltale/pkg/definition"
)

// committedCRD is the CRD file `task generate` wrote.
var committedCRD = filepath.Join("..", "..", definition.CRDFile)

// crdSchema is the committed CRD's openAPIV3Schema as the API server holds it.
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

var _ = Describe("the TraceDefinition CRD", func() {
	It("is the one `task generate` writes from the types", func() {
		generated, err := definition.CRD()
		Expect(err).ToNot(HaveOccurred())
		committed, err := os.ReadFile(committedCRD)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(committed)).To(Equal(string(generated)), "run `task generate`")
	})

	It("is a structural schema the API server accepts", func() {
		structural, err := structuralschema.NewStructural(crdSchema())
		Expect(err).ToNot(HaveOccurred())
		Expect(structuralschema.ValidateStructural(nil, structural)).To(BeEmpty())
	})

	for _, example := range examples {
		It("validates "+example+" with nothing pruned", func() {
			data, err := os.ReadFile(example)
			Expect(err).ToNot(HaveOccurred())
			var object map[string]any
			Expect(yaml.Unmarshal(data, &object)).To(Succeed())

			schema := crdSchema()
			validator, _, err := validation.NewSchemaValidator(schema)
			Expect(err).ToNot(HaveOccurred())
			Expect(validation.ValidateCustomResource(nil, object, validator)).To(BeEmpty())

			structural, err := structuralschema.NewStructural(schema)
			Expect(err).ToNot(HaveOccurred())
			pruned := pruning.PruneWithOptions(object, structural, true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})
			Expect(pruned).To(BeEmpty())
		})
	}
})
