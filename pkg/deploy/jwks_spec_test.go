package deploy_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/flanksource/telltale/pkg/deploy"
	"github.com/flanksource/telltale/pkg/deploy/auth"
)

var _ = Describe("deployer JWKS", func() {
	const issuerName = "connection://shop/signing"
	var issuer *auth.Issuer
	var other auth.JWK

	BeforeEach(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		issuer, err = auth.NewIssuer(issuerName, key)
		Expect(err).NotTo(HaveOccurred())
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		shared, err := auth.NewIssuer("connection://shop/shared", otherKey)
		Expect(err).NotTo(HaveOccurred())
		other = shared.JWKS().Keys[0]
	})

	deployer := func(source *deploy.JWKSSource) deploy.Deployer {
		return deploy.Deployer{Spec: deploy.Spec{Selector: deploy.Selector{Namespace: "shop"}, Auth: deploy.AuthSpec{Issuer: issuerName, JWKS: source}}}
	}
	document := func(keys ...auth.JWK) string {
		data, err := json.Marshal(auth.JWKS{Keys: keys})
		Expect(err).NotTo(HaveOccurred())
		return string(data)
	}

	It("is the issuer's own keys when no object is named", func() {
		jwks, err := deploy.AgentJWKS(context.Background(), fake.NewClientset(), deployer(nil), issuer)
		Expect(err).NotTo(HaveOccurred())
		Expect(jwks).To(Equal(issuer.JWKS()))
	})

	It("publishes the issuer's keys to a ConfigMap it creates, then to the same one on the next deploy", func() {
		client := fake.NewClientset()
		source := &deploy.JWKSSource{Mode: deploy.JWKSPublish, Kind: "ConfigMap", Name: "telltale-jwks"}
		for range 2 {
			jwks, err := deploy.AgentJWKS(context.Background(), client, deployer(source), issuer)
			Expect(err).NotTo(HaveOccurred())
			Expect(jwks).To(Equal(issuer.JWKS()))
		}
		published, err := client.CoreV1().ConfigMaps("shop").Get(context.Background(), "telltale-jwks", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(published.Data).To(Equal(map[string]string{"jwks.json": document(issuer.JWKS().Keys...)}))
		Expect(published.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "telltale"))
	})

	It("publishes to a Secret key it names", func() {
		client := fake.NewClientset()
		_, err := deploy.AgentJWKS(context.Background(), client, deployer(&deploy.JWKSSource{Mode: deploy.JWKSPublish, Kind: "Secret", Name: "keys", Key: "agents.jwks"}), issuer)
		Expect(err).NotTo(HaveOccurred())
		secret, err := client.CoreV1().Secrets("shop").Get(context.Background(), "keys", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(secret.Data["agents.jwks"])).To(Equal(document(issuer.JWKS().Keys...)))
	})

	It("reads the keys another issuer already published, when they include this issuer's signing key", func() {
		client := fake.NewClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shared-jwks", Namespace: "shop"}, Data: map[string][]byte{"jwks.json": []byte(document(other, issuer.JWKS().Keys[0]))}})
		jwks, err := deploy.AgentJWKS(context.Background(), client, deployer(&deploy.JWKSSource{Mode: deploy.JWKSRead, Kind: "Secret", Name: "shared-jwks"}), issuer)
		Expect(err).NotTo(HaveOccurred())
		Expect(jwks.Keys).To(Equal([]auth.JWK{other, issuer.JWKS().Keys[0]}))
	})

	DescribeTable("refuses a JWKS source it cannot provision",
		func(source deploy.JWKSSource, objects []corev1.ConfigMap, message string) {
			client := fake.NewClientset()
			for i := range objects {
				_, err := client.CoreV1().ConfigMaps("shop").Create(context.Background(), &objects[i], metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := deploy.AgentJWKS(context.Background(), client, deployer(&source), issuer)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an unknown mode", deploy.JWKSSource{Mode: "sync", Kind: "ConfigMap", Name: "k"}, nil, `jwks mode "sync"`),
		Entry("an unknown kind", deploy.JWKSSource{Mode: deploy.JWKSRead, Kind: "Vault", Name: "k"}, nil, `jwks kind "Vault"`),
		Entry("no name", deploy.JWKSSource{Mode: deploy.JWKSRead, Kind: "ConfigMap"}, nil, "jwks name is required"),
		Entry("a missing object", deploy.JWKSSource{Mode: deploy.JWKSRead, Kind: "ConfigMap", Name: "absent"}, nil, "read ConfigMap shop/absent"),
		Entry("a missing key", deploy.JWKSSource{Mode: deploy.JWKSRead, Kind: "ConfigMap", Name: "keys"},
			[]corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: "shop"}, Data: map[string]string{"other": "{}"}}}, `ConfigMap shop/keys has no key "jwks.json"`),
		Entry("keys without the issuer's", deploy.JWKSSource{Mode: deploy.JWKSRead, Kind: "ConfigMap", Name: "keys"},
			[]corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "keys", Namespace: "shop"}, Data: map[string]string{"jwks.json": `{"keys":[]}`}}}, "does not hold the signing key"),
	)
})
