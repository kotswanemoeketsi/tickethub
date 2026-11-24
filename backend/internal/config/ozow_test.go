package config

import (
	"encoding/base64"
	"testing"
)

func TestOzowConfiguration(t *testing.T) {
	valid := Config{Env: "production", JWTSecret: devSecretWarning, QRSigningKey: devSecretWarning,
		DefaultTimezone: "Africa/Johannesburg", DefaultCurrency: "ZAR", PasswordIterations: 600000, CookieSecure: true,
		SMTPTLS: "starttls", PaymentProvider: "ozow", OzowClientID: "client", OzowClientSecret: "secret", OzowSiteCode: "SITE",
		OzowWebhookSecret: "whsec_" + base64.StdEncoding.EncodeToString([]byte("signing-secret")),
		OzowBaseURL:       "https://one.ozow.com/v1", PublicURL: "https://gotaticket.co.za", APIURL: "https://gotaticket.co.za"}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"retired provider", func(c *Config) { c.PaymentProvider = "paystack" }},
		{"no secret", func(c *Config) { c.OzowClientSecret = "" }},
		{"bad signing key", func(c *Config) { c.OzowWebhookSecret = "whsec_invalid!" }},
		{"wrong currency", func(c *Config) { c.DefaultCurrency = "USD" }},
		{"insecure callback", func(c *Config) { c.PublicURL = "http://gotaticket.co.za" }},
		{"untrusted endpoint", func(c *Config) { c.OzowBaseURL = "https://other.example/v1" }},
		{"staging in production", func(c *Config) { c.OzowBaseURL = "https://stagingone.ozow.com/v1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.change(&c)
			if c.validate() == nil {
				t.Fatal("accepted invalid Ozow configuration")
			}
		})
	}
}
