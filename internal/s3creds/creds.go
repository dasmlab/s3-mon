package s3creds

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

// Creds is the resolved S3 connection configuration.
type Creds struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	// Insecure means plain HTTP (Thanos semantics), not "skip TLS verify".
	Insecure           bool
	InsecureSkipVerify bool
	ForcePathStyle     bool
	// CABundle is extra PEM trusted on top of the system/cluster roots.
	CABundle []byte
}

// FromSecret parses a Secret using Thanos-style discrete keys (preferred),
// AWS_* aliases, or a Thanos objstore `config` YAML blob.
func FromSecret(sec *corev1.Secret) (Creds, error) {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := sec.Data[k]; ok && len(v) > 0 {
				return strings.TrimSpace(string(v))
			}
		}
		return ""
	}

	c := Creds{
		Endpoint:  get("endpoint", "Endpoint", "AWS_ENDPOINT", "AWS_S3_ENDPOINT"),
		AccessKey: get("access_key", "accessKey", "AWS_ACCESS_KEY_ID", "access-key"),
		SecretKey: get("secret_key", "secretKey", "AWS_SECRET_ACCESS_KEY", "secret-key"),
		Region:    get("region", "Region", "AWS_REGION", "AWS_DEFAULT_REGION"),
	}

	if raw := get("config", "thanos.yaml"); raw != "" {
		if err := mergeThanosConfig(&c, raw); err != nil {
			return Creds{}, err
		}
	}

	if v := get("insecure", "Insecure"); v != "" {
		c.Insecure, _ = strconv.ParseBool(v)
	}
	if v := get("insecure_skip_verify", "insecureSkipVerify", "InsecureSkipVerify"); v != "" {
		c.InsecureSkipVerify, _ = strconv.ParseBool(v)
	}
	if v := get("forcePathStyle", "force_path_style", "ForcePathStyle"); v != "" {
		c.ForcePathStyle, _ = strconv.ParseBool(v)
	}
	if v := get("ca.crt", "ca-bundle.crt", "ca_bundle", "caBundle"); v != "" {
		c.CABundle = []byte(v)
	}

	if c.AccessKey == "" || c.SecretKey == "" {
		return Creds{}, fmt.Errorf("secret %s/%s missing access_key/secret_key (or AWS_* aliases)", sec.Namespace, sec.Name)
	}
	if c.Endpoint == "" {
		return Creds{}, fmt.Errorf("secret %s/%s missing endpoint", sec.Namespace, sec.Name)
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	// Strip scheme if someone pasted https://host:port
	c.Endpoint = strings.TrimPrefix(strings.TrimPrefix(c.Endpoint, "https://"), "http://")
	c.Endpoint = strings.TrimSuffix(c.Endpoint, "/")
	return c, nil
}

type thanosRoot struct {
	Type   string         `yaml:"type"`
	Config thanosS3Config `yaml:"config"`
}

type thanosS3Config struct {
	Endpoint       string `yaml:"endpoint"`
	AccessKey      string `yaml:"access_key"`
	SecretKey      string `yaml:"secret_key"`
	Region         string `yaml:"region"`
	Insecure       bool   `yaml:"insecure"`
	ForcePathStyle *bool  `yaml:"force_path_style"`
	HTTPConfig     struct {
		InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
		TLSConfig          struct {
			InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
		} `yaml:"tls_config"`
	} `yaml:"http_config"`
}

func mergeThanosConfig(c *Creds, raw string) error {
	var root thanosRoot
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		// bare config map without type wrapper
		var cfg thanosS3Config
		if err2 := yaml.Unmarshal([]byte(raw), &cfg); err2 != nil {
			return fmt.Errorf("parse config key: %w", err)
		}
		root.Config = cfg
	}
	cfg := root.Config
	if cfg.Endpoint != "" {
		c.Endpoint = cfg.Endpoint
	}
	if cfg.AccessKey != "" {
		c.AccessKey = cfg.AccessKey
	}
	if cfg.SecretKey != "" {
		c.SecretKey = cfg.SecretKey
	}
	if cfg.Region != "" {
		c.Region = cfg.Region
	}
	if cfg.Insecure {
		c.Insecure = true
	}
	if cfg.ForcePathStyle != nil {
		c.ForcePathStyle = *cfg.ForcePathStyle
	}
	if cfg.HTTPConfig.InsecureSkipVerify || cfg.HTTPConfig.TLSConfig.InsecureSkipVerify {
		c.InsecureSkipVerify = true
	}
	return nil
}
