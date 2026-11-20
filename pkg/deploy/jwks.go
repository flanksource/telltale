package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/flanksource/telltale/pkg/deploy/auth"
)

const defaultJWKSKey = "jwks.json"

func (s JWKSSource) validate() error {
	switch {
	case s.Mode != JWKSRead && s.Mode != JWKSPublish:
		return fmt.Errorf("jwks mode %q is not %s or %s", s.Mode, JWKSRead, JWKSPublish)
	case s.Kind != "ConfigMap" && s.Kind != "Secret":
		return fmt.Errorf("jwks kind %q is not ConfigMap or Secret", s.Kind)
	case s.Name == "":
		return errors.New("jwks name is required")
	}
	return nil
}

func (s JWKSSource) key() string {
	if s.Key == "" {
		return defaultJWKSKey
	}
	return s.Key
}

// AgentJWKS is the JWKS a deployer's agents verify tokens with: the issuer's
// own, or the one its Secret or ConfigMap holds once read, or published to it.
func AgentJWKS(ctx context.Context, client kubernetes.Interface, deployer Deployer, issuer *auth.Issuer) (auth.JWKS, error) {
	source := deployer.Spec.Auth.JWKS
	if source == nil {
		return issuer.JWKS(), nil
	}
	if err := source.validate(); err != nil {
		return auth.JWKS{}, err
	}
	namespace := deployer.Spec.Selector.Namespace
	if source.Mode == JWKSPublish {
		return issuer.JWKS(), publishJWKS(ctx, client, namespace, deployer.Name, *source, issuer.JWKS())
	}
	data, err := readJWKS(ctx, client, namespace, *source)
	if err != nil {
		return auth.JWKS{}, err
	}
	var jwks auth.JWKS
	if err := json.Unmarshal(data, &jwks); err != nil {
		return auth.JWKS{}, fmt.Errorf("%s %s/%s key %s is not a JWKS: %w", source.Kind, namespace, source.Name, source.key(), err)
	}
	signing := issuer.JWKS().Keys[0].KeyID
	if !slices.ContainsFunc(jwks.Keys, func(key auth.JWK) bool { return key.KeyID == signing }) {
		return auth.JWKS{}, fmt.Errorf("%s %s/%s does not hold the signing key %s of %s, so agents would refuse its tokens", source.Kind, namespace, source.Name, signing, issuer.Name())
	}
	return jwks, nil
}

func readJWKS(ctx context.Context, client kubernetes.Interface, namespace string, source JWKSSource) ([]byte, error) {
	var data []byte
	var found bool
	switch source.Kind {
	case "ConfigMap":
		object, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("read ConfigMap %s/%s: %w", namespace, source.Name, err)
		}
		var text string
		text, found = object.Data[source.key()]
		data = []byte(text)
	default:
		object, err := client.CoreV1().Secrets(namespace).Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("read Secret %s/%s: %w", namespace, source.Name, err)
		}
		data, found = object.Data[source.key()]
	}
	if !found {
		return nil, fmt.Errorf("%s %s/%s has no key %q", source.Kind, namespace, source.Name, source.key())
	}
	return data, nil
}

// publishJWKS writes jwks to the source's key, creating the object labelled
// as telltale's when it does not exist yet.
func publishJWKS(ctx context.Context, client kubernetes.Interface, namespace, deployer string, source JWKSSource, jwks auth.JWKS) error {
	data, err := json.Marshal(jwks)
	if err != nil {
		return err
	}
	meta := metav1.ObjectMeta{Name: source.Name, Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "telltale", "telltale.flanksource.com/deployer": deployer}}
	if source.Kind == "ConfigMap" {
		configMaps := client.CoreV1().ConfigMaps(namespace)
		existing, err := configMaps.Get(ctx, source.Name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			_, err = configMaps.Create(ctx, &corev1.ConfigMap{ObjectMeta: meta, Data: map[string]string{source.key(): string(data)}}, metav1.CreateOptions{})
		case err == nil:
			if existing.Data == nil {
				existing.Data = map[string]string{}
			}
			existing.Data[source.key()] = string(data)
			_, err = configMaps.Update(ctx, existing, metav1.UpdateOptions{})
		}
		if err != nil {
			return fmt.Errorf("publish JWKS to ConfigMap %s/%s: %w", namespace, source.Name, err)
		}
		return nil
	}
	secrets := client.CoreV1().Secrets(namespace)
	existing, err := secrets.Get(ctx, source.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = secrets.Create(ctx, &corev1.Secret{ObjectMeta: meta, Data: map[string][]byte{source.key(): data}}, metav1.CreateOptions{})
	case err == nil:
		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		existing.Data[source.key()] = data
		_, err = secrets.Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("publish JWKS to Secret %s/%s: %w", namespace, source.Name, err)
	}
	return nil
}
