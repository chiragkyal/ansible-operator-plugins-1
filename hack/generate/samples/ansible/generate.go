// Copyright 2021 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ansible

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/operator-framework/ansible-operator-plugins/hack/generate/samples/internal/pkg"
	"github.com/operator-framework/ansible-operator-plugins/pkg/testutils/command"
	"github.com/operator-framework/ansible-operator-plugins/pkg/testutils/e2e"
	"github.com/operator-framework/ansible-operator-plugins/pkg/testutils/sample"
)

const bundleImage = "quay.io/example/memcached-operator:v0.0.1"

var memcachedGVK = schema.GroupVersionKind{
	Group:   "cache",
	Version: "v1alpha1",
	Kind:    "Memcached",
}

// ExecSample implements sample.Sample by invoking the external ansible-cli binary.
type ExecSample struct {
	baseDir        string
	name           string
	domain         string
	plugins        []string
	gvks           []schema.GroupVersionKind
	initOptions    []string
	apiOptions     []string
	webhookOptions []string
	binary         string
	env            []string
}

func (s *ExecSample) CommandContext() command.CommandContext {
	// Used for post-generation cleanup and file operations
	return command.NewGenericCommandContext()
}

func (s *ExecSample) Name() string                    { return s.name }
func (s *ExecSample) Domain() string                  { return s.domain }
func (s *ExecSample) GVKs() []schema.GroupVersionKind { return s.gvks }
func (s *ExecSample) Dir() string                     { return filepath.Join(s.baseDir, s.name) }
func (s *ExecSample) Binary() string                  { return s.binary }

func (s *ExecSample) GenerateInit() error {
	dir := s.Dir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating directory %s: %w", dir, err)
	}
	args := []string{"init", "--plugins", strings.Join(s.plugins, ","), "--domain", s.domain}
	args = append(args, s.initOptions...)
	cmd := exec.Command(s.binary, args...)
	cmd.Env = append(os.Environ(), s.env...)
	cmd.Dir = dir
	fmt.Println("Running command:", strings.Join(cmd.Args, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running %v: %w\n%s", cmd.Args, err, string(output))
	}
	return nil
}

func (s *ExecSample) GenerateApi() error {
	dir := s.Dir()
	for _, gvk := range s.gvks {
		args := []string{"create", "api", "--plugins", strings.Join(s.plugins, ","), "--group", gvk.Group, "--version", gvk.Version, "--kind", gvk.Kind}
		args = append(args, s.apiOptions...)
		cmd := exec.Command(s.binary, args...)
		cmd.Env = append(os.Environ(), s.env...)
		cmd.Dir = dir
		fmt.Println("Running command:", strings.Join(cmd.Args, " "))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running %v: %w\n%s", cmd.Args, err, string(output))
		}
	}
	return nil
}

func (s *ExecSample) GenerateWebhook() error {
	dir := s.Dir()
	for _, gvk := range s.gvks {
		args := []string{"create", "webhook", "--plugins", strings.Join(s.plugins, ","), "--group", gvk.Group, "--version", gvk.Version, "--kind", gvk.Kind}
		args = append(args, s.webhookOptions...)
		cmd := exec.Command(s.binary, args...)
		cmd.Env = append(os.Environ(), s.env...)
		cmd.Dir = dir
		fmt.Println("Running command:", strings.Join(cmd.Args, " "))
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running %v: %w\n%s", cmd.Args, err, string(output))
		}
	}
	return nil
}

// GenerateMemcachedSamples scaffolds a Memcached operator using the local ansible-cli binary.
func GenerateMemcachedSamples(rootPath string) []sample.Sample {
	samplesPath := filepath.Join(rootPath, "ansible")
	s := &ExecSample{
		baseDir:     samplesPath,
		name:        "memcached-operator",
		domain:      "example.com",
		plugins:     []string{"ansible"},
		gvks:        []schema.GroupVersionKind{memcachedGVK},
		initOptions: []string{},
		apiOptions:  []string{"--generate-role", "--generate-playbook"},
		binary:      "ansible-cli",
		env:         []string{"GO111MODULE=on"},
	}
	pkg.CheckError("attempting to remove sample dir", os.RemoveAll(s.Dir()))
	gen := sample.NewGenerator(sample.WithNoWebhook())
	pkg.CheckError("generating ansible samples", gen.GenerateSamples(s))
	ImplementMemcached(s, bundleImage)
	return []sample.Sample{s}
}

// GenerateMoleculeSample scaffolds a multi-CR sample using the local ansible-cli binary.
func GenerateMoleculeSample(samplesPath string) []sample.Sample {
	s := &ExecSample{
		baseDir: samplesPath,
		name:    "memcached-molecule-operator",
		domain:  "example.com",
		plugins: []string{"ansible"},
		gvks: []schema.GroupVersionKind{
			memcachedGVK,
			{Group: memcachedGVK.Group, Version: memcachedGVK.Version, Kind: "Foo"},
			{Group: memcachedGVK.Group, Version: memcachedGVK.Version, Kind: "Memfin"},
		},
		initOptions: []string{},
		apiOptions:  []string{"--generate-role", "--generate-playbook"},
		binary:      "ansible-cli",
		env:         []string{"GO111MODULE=on"},
	}
	ignore := &ExecSample{
		baseDir:     samplesPath,
		name:        s.name,
		domain:      s.domain,
		plugins:     []string{"ansible"},
		gvks:        []schema.GroupVersionKind{{Group: "ignore", Version: "v1", Kind: "Secret"}},
		initOptions: []string{},
		apiOptions:  []string{"--generate-role"},
		binary:      "ansible-cli",
		env:         []string{"GO111MODULE=on"},
	}
	pkg.CheckError("attempting to remove sample dir", os.RemoveAll(s.Dir()))
	gen := sample.NewGenerator(sample.WithNoWebhook())
	pkg.CheckError("generating ansible molecule sample", gen.GenerateSamples(s))
	log.Infof("enabling multigroup support")
	pkg.CheckError("updating PROJECT file", e2e.AllowProjectBeMultiGroup(s))
	ignoreGen := sample.NewGenerator(sample.WithNoInit(), sample.WithNoWebhook())
	pkg.CheckError("generating ansible molecule sample - ignore", ignoreGen.GenerateSamples(ignore))
	ImplementMemcached(s, bundleImage)
	ImplementMemcachedMolecule(s, bundleImage)
	return []sample.Sample{s}
}

// GenerateAdvancedMoleculeSample scaffolds an advanced multi-CR sample using the local ansible-cli binary.
func GenerateAdvancedMoleculeSample(samplesPath string) {
	gv := schema.GroupVersion{Group: "test", Version: "v1alpha1"}
	kinds := []string{"ArgsTest", "CaseTest", "CollectionTest", "ClusterAnnotationTest", "FinalizerConcurrencyTest", "ReconciliationTest", "SelectorTest", "SubresourcesTest"}
	var gvks []schema.GroupVersionKind
	for _, kind := range kinds {
		gvks = append(gvks, schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: kind})
	}
	s := &ExecSample{
		baseDir:     samplesPath,
		name:        "advanced-molecule-operator",
		domain:      "example.com",
		plugins:     []string{"ansible"},
		gvks:        gvks,
		initOptions: []string{},
		apiOptions:  []string{"--generate-playbook"},
		binary:      "ansible-cli",
		env:         []string{"GO111MODULE=on"},
	}
	inventory := &ExecSample{
		baseDir:     samplesPath,
		name:        s.name,
		domain:      s.domain,
		plugins:     []string{"ansible"},
		gvks:        []schema.GroupVersionKind{{Group: gv.Group, Version: gv.Version, Kind: "InventoryTest"}},
		initOptions: []string{},
		apiOptions:  []string{"--generate-role", "--generate-playbook"},
		binary:      "ansible-cli",
		env:         []string{"GO111MODULE=on"},
	}
	pkg.CheckError("attempting to remove sample dir", os.RemoveAll(s.Dir()))
	gen := sample.NewGenerator(sample.WithNoWebhook())
	pkg.CheckError("generating ansible advanced molecule sample", gen.GenerateSamples(s))
	ignoreGen := sample.NewGenerator(sample.WithNoInit(), sample.WithNoWebhook())
	pkg.CheckError("generating ansible molecule sample - ignore", ignoreGen.GenerateSamples(inventory))
	ImplementAdvancedMolecule(s, bundleImage)
}
