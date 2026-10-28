package telltale

import (
	"encoding/json"

	"github.com/flanksource/commons/logger"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("secret masking", func() {
	It("masks separate flags, sensitive headers and assignments without mutating arguments", func() {
		args := []string{"mysql", "--password", "hunter2", "-p", "pass-value", "--token", "token-value", "--secret", "secret-value", "--api-key", "key-value", "-H", "Authorization: Bearer private", "--header", "Cookie: session=private", "API_TOKEN=private", "--header", "Accept: application/json", "--port", "5432"}
		Expect(MaskArgs(args)).To(Equal([]string{"mysql", "--password", "****", "-p", "****", "--token", "****", "--secret", "****", "--api-key", "****", "-H", "Authorization: ****", "--header", "Cookie: ****", "API_TOKEN=****", "--header", "Accept: application/json", "--port", "5432"}))
		Expect(args[2]).To(Equal("hunter2"))
		Expect(MaskArgs([]string{"psql", "-p", "5432"})).To(Equal([]string{"psql", "-p", "5432"}))
		Expect(MaskArgs([]string{"mysql", "-psecret"})).To(Equal([]string{"mysql", "-p****"}))
		Expect(MaskArgs([]string{"curl", "--header=Cookie: session=private"})).To(Equal([]string{"curl", "--header=Cookie: ****"}))
	})
	It("preserves StripSecrets behavior and masks secrets in nested JSON strings", func() {
		input := "https://example.test/?token=private"
		Expect(Mask(input)).To(Equal(logger.StripSecrets(input)))
		masked := Mask(`{"password":"hunter2","nested":{"token":"private","text":"password=private"}}`)
		var value map[string]any
		Expect(json.Unmarshal([]byte(masked), &value)).To(Succeed())
		Expect(masked).NotTo(ContainSubstring("hunter2"))
		Expect(masked).NotTo(ContainSubstring("private"))
		Expect(Mask(`command --password "two words"`)).NotTo(ContainSubstring("two words"))
		Expect(Mask("Cookie: session=private; other=value")).NotTo(ContainSubstring("private"))
		Expect(Mask(`{"name":"Authorization","value":"private"}`)).NotTo(ContainSubstring("private"))
	})
})
