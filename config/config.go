// Licensed under the Apache License, Version 2.0
// Details: https://raw.githubusercontent.com/maniksurtani/quotaservice/master/LICENSE

// Package implements configs for the quotaservice
package config

import (
	"fmt"
	"io"
	"io/ioutil"

	"gopkg.in/yaml.v2"

	"encoding/json"

	"bytes"
	"github.com/golang/protobuf/proto"
	"github.com/maniksurtani/quotaservice/logging"
	pb "github.com/maniksurtani/quotaservice/protos/config"
)

const (
	GlobalNamespace           = "___GLOBAL___"
	DefaultBucketName         = "___DEFAULT_BUCKET___"
	DynamicBucketTemplateName = "___DYNAMIC_BUCKET_TPL___"
)

type ServiceConfig struct {
	GlobalDefaultBucket *BucketConfig               `yaml:"global_default_bucket,flow"`
	Namespaces          map[string]*NamespaceConfig `yaml:",flow"`
	Version             int
}

func (s *ServiceConfig) String() string {
	return fmt.Sprintf("ServiceConfig{default: %v, namespaces: %v}",
		s.GlobalDefaultBucket, s.Namespaces)
}

func (s *ServiceConfig) AddNamespace(namespace string, n *NamespaceConfig) *ServiceConfig {
	s.Namespaces[namespace] = n
	n.Name = namespace
	return s
}

func (s *ServiceConfig) Equals(other *ServiceConfig) bool {
	// TODO(manik) we need a better impl! What's idiomatic?
	p1 := s.ToProto()
	p2 := other.ToProto()

	return proto.Equal(p1, p2)
}

func (s *ServiceConfig) ToProto() *pb.ServiceConfig {
	return &pb.ServiceConfig{
		Version:             int32(s.Version),
		GlobalDefaultBucket: bucketToProto(DefaultBucketName, s.GlobalDefaultBucket),
		Namespaces:          namespaceMapToProto(s.Namespaces)}
}

func (s *ServiceConfig) ApplyDefaults() *ServiceConfig {
	if s.GlobalDefaultBucket != nil {
		s.GlobalDefaultBucket.ApplyDefaults()
		s.GlobalDefaultBucket.Name = DefaultBucketName
	}

	for name, ns := range s.Namespaces {
		ns.Name = name
		if ns.DefaultBucket != nil && ns.DynamicBucketTemplate != nil {
			panic(fmt.Sprintf("Namespace %v is not allowed to have a default bucket as well as allow dynamic buckets.", name))
		}

		// Ensure the namespace's bucket map exists.
		if ns.Buckets == nil {
			ns.Buckets = make(map[string]*BucketConfig)
		}

		if ns.DefaultBucket != nil {
			ns.DefaultBucket.ApplyDefaults()
			ns.DefaultBucket.Name = DefaultBucketName
			ns.DefaultBucket.namespace = ns
		}

		if ns.DynamicBucketTemplate != nil {
			ns.DynamicBucketTemplate.ApplyDefaults()
			ns.DynamicBucketTemplate.Name = DynamicBucketTemplateName
			ns.DynamicBucketTemplate.namespace = ns
		}

		for n, b := range ns.Buckets {
			b.ApplyDefaults()
			b.Name = n
			b.namespace = ns
		}
	}

	return s
}

func (s *ServiceConfig) NamespaceNames() (names []string) {
	if s.Namespaces == nil || len(s.Namespaces) == 0 {
		return []string{}
	}

	names = make([]string, 0, len(s.Namespaces))
	for ns, _ := range s.Namespaces {
		names = append(names, ns)
	}

	return
}

type NamespaceConfig struct {
	DefaultBucket         *BucketConfig            `yaml:"default_bucket,flow"`
	DynamicBucketTemplate *BucketConfig            `yaml:"dynamic_bucket_template,flow"`
	MaxDynamicBuckets     int                      `yaml:"max_dynamic_buckets"`
	Buckets               map[string]*BucketConfig `yaml:",flow"`
	Name                  string
}

func (n *NamespaceConfig) AddBucket(name string, b *BucketConfig) *NamespaceConfig {
	n.Buckets[name] = b
	b.Name = name
	b.namespace = n
	return n
}

func (n *NamespaceConfig) SetDynamicBucketTemplate(b *BucketConfig) *NamespaceConfig {
	b.Name = DynamicBucketTemplateName
	b.namespace = n
	n.DynamicBucketTemplate = b
	return n
}

func (n *NamespaceConfig) ToProto() *pb.NamespaceConfig {
	return &pb.NamespaceConfig{
		DefaultBucket:         bucketToProto(DefaultBucketName, n.DefaultBucket),
		DynamicBucketTemplate: bucketToProto(DynamicBucketTemplateName, n.DynamicBucketTemplate),
		MaxDynamicBuckets:     int32(n.MaxDynamicBuckets),
		Buckets:               bucketMapToProto(n.Buckets),
		Name:                  n.Name}
}

type BucketConfig struct {
	Size                int64
	FillRate            int64 `yaml:"fill_rate"`
	WaitTimeoutMillis   int64 `yaml:"wait_timeout_millis"`
	MaxIdleMillis       int64 `yaml:"max_idle_millis"`
	MaxDebtMillis       int64 `yaml:"max_debt_millis"`
	MaxTokensPerRequest int64 `yaml:"max_tokens_per_request"`
	namespace           *NamespaceConfig
	Name                string
}

func (b *BucketConfig) String() string {
	return fmt.Sprint(*b)
}

func (b *BucketConfig) ToProto() *pb.BucketConfig {
	return &pb.BucketConfig{
		Size:                b.Size,
		FillRate:            b.FillRate,
		WaitTimeoutMillis:   b.WaitTimeoutMillis,
		MaxIdleMillis:       b.MaxIdleMillis,
		MaxDebtMillis:       b.MaxDebtMillis,
		MaxTokensPerRequest: b.MaxTokensPerRequest,
		Name:                b.Name}
}

func (b *BucketConfig) ApplyDefaults() *BucketConfig {
	if b.Size == 0 {
		b.Size = 100
	}

	if b.FillRate == 0 {
		b.FillRate = 50
	}

	if b.WaitTimeoutMillis == 0 {
		b.WaitTimeoutMillis = 1000
	}

	if b.MaxIdleMillis == 0 {
		b.MaxIdleMillis = -1
	}

	if b.MaxDebtMillis == 0 {
		b.MaxDebtMillis = 10000
	}

	if b.MaxTokensPerRequest == 0 {
		b.MaxTokensPerRequest = b.FillRate
	}

	return b
}

func (b *BucketConfig) FQN() string {
	if b.namespace == nil {
		// This is a global default.
		return FullyQualifiedName(GlobalNamespace, DefaultBucketName)
	}

	return FullyQualifiedName(b.namespace.Name, b.Name)
}

func ReadConfigFromFile(filename string) *ServiceConfig {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		panic(fmt.Sprintf("Unable to open file %v. Error: %v", filename, err))
	}

	return readConfigFromBytes(bytes)
}

func ReadConfig(yamlStream io.Reader) *ServiceConfig {
	bytes, err := ioutil.ReadAll(yamlStream)
	if err != nil {
		panic(fmt.Sprintf("Unable to open reader. Error: %v", err))
	}

	return readConfigFromBytes(bytes)
}

func readConfigFromBytes(bytes []byte) *ServiceConfig {
	logging.Print(string(bytes))
	cfg := NewDefaultServiceConfig()
	cfg.GlobalDefaultBucket = nil
	yaml.Unmarshal(bytes, cfg)

	return cfg.ApplyDefaults()
}

func NewDefaultServiceConfig() *ServiceConfig {
	return &ServiceConfig{
		NewDefaultBucketConfig(),
		make(map[string]*NamespaceConfig),
		0}
}

func NewDefaultNamespaceConfig() *NamespaceConfig {
	return &NamespaceConfig{Buckets: make(map[string]*BucketConfig)}
}

func NewDefaultBucketConfig() *BucketConfig {
	return &BucketConfig{Size: 100, FillRate: 50, WaitTimeoutMillis: 1000, MaxIdleMillis: -1, MaxDebtMillis: 10000}
}

// Helpers to read to and write from proto representations
func bucketToProto(name string, b *BucketConfig) *pb.BucketConfig {
	if b == nil {
		return nil
	}

	return b.ToProto()
}

func bucketMapToProto(buckets map[string]*BucketConfig) []*pb.BucketConfig {
	c := make([]*pb.BucketConfig, 0, len(buckets))
	for n, b := range buckets {
		c = append(c, bucketToProto(n, b))
	}

	return c
}

func namespaceMapToProto(namespaces map[string]*NamespaceConfig) []*pb.NamespaceConfig {
	c := make([]*pb.NamespaceConfig, 0, len(namespaces))
	for _, nsp := range namespaces {
		c = append(c, nsp.ToProto())
	}

	return c
}

func FromProto(cfg *pb.ServiceConfig) *ServiceConfig {
	globalBucket := BucketFromProto(cfg.GlobalDefaultBucket, nil)
	return &ServiceConfig{
		GlobalDefaultBucket: globalBucket,
		Version:             int(cfg.Version),
		Namespaces:          namespacesFromProto(cfg.Namespaces)}
}

func FromJSON(j []byte) (c *ServiceConfig, e error) {
	p := &pb.ServiceConfig{}
	e = json.Unmarshal(j, p)
	if e == nil {
		c = FromProto(p)
	}

	return
}

func bucketsFromProto(cfgs []*pb.BucketConfig, nsc *NamespaceConfig) map[string]*BucketConfig {
	buckets := make(map[string]*BucketConfig, len(cfgs))
	for _, cfg := range cfgs {
		b := BucketFromProto(cfg, nsc)
		if b != nil {
			buckets[b.Name] = b
		}
	}

	return buckets
}

func BucketFromProto(cfg *pb.BucketConfig, nsc *NamespaceConfig) (b *BucketConfig) {
	if cfg == nil {
		return
	}

	b = &BucketConfig{
		Size:                cfg.Size,
		FillRate:            cfg.FillRate,
		WaitTimeoutMillis:   cfg.WaitTimeoutMillis,
		MaxIdleMillis:       cfg.MaxIdleMillis,
		MaxDebtMillis:       cfg.MaxDebtMillis,
		MaxTokensPerRequest: cfg.MaxTokensPerRequest,
		namespace:           nsc, Name: cfg.Name}
	return
}

func namespacesFromProto(cfgs []*pb.NamespaceConfig) map[string]*NamespaceConfig {
	namespaces := make(map[string]*NamespaceConfig, len(cfgs))

	for _, cfg := range cfgs {
		ns := NamespaceFromProto(cfg)
		if ns != nil {
			namespaces[ns.Name] = ns
		}
	}

	return namespaces
}

func NamespaceFromProto(cfg *pb.NamespaceConfig) (n *NamespaceConfig) {
	if cfg == nil {
		return
	}

	n = &NamespaceConfig{
		MaxDynamicBuckets: int(cfg.MaxDynamicBuckets),
		Name:              cfg.Name}

	n.DefaultBucket = BucketFromProto(cfg.DefaultBucket, n)
	n.DynamicBucketTemplate = BucketFromProto(cfg.DynamicBucketTemplate, n)
	n.Buckets = bucketsFromProto(cfg.Buckets, n)

	return
}

func NamespaceFromJSON(j []byte) (n *NamespaceConfig, e error) {
	p := &pb.NamespaceConfig{}
	e = json.Unmarshal(j, p)
	if e == nil {
		n = NamespaceFromProto(p)
	}

	return
}

func FullyQualifiedName(namespace, bucketName string) string {
	return namespace + ":" + bucketName
}

func Marshal(s *ServiceConfig) (io.Reader, error) {
	p := s.ToProto()
	b, e := proto.Marshal(p)
	if e != nil {
		return nil, e
	}

	return bytes.NewReader(b), nil
}

func Unmarshal(r io.Reader) (*ServiceConfig, error) {
	b, e := ioutil.ReadAll(r)
	if e != nil {
		return nil, e
	}

	p := &pb.ServiceConfig{}
	e = proto.Unmarshal(b, p)
	if e != nil {
		return nil, e
	}

	return FromProto(p), nil
}
