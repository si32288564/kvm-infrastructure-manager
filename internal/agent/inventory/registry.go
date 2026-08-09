package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
)

type ModuleDescriptor struct {
	Name            string
	Version         string
	Domain          Domain
	SchemaVersion   string
	ArtifactDigest  string
	CapabilityNames []string
}

type Module interface {
	Descriptor() ModuleDescriptor
	Collect(context.Context) (Fragment, error)
}

// Registry owns typed inventory modules, seals registration before collection,
// and normalizes their concurrent observations into one stable snapshot.
type Registry struct {
	mu      sync.Mutex
	modules map[string]Module
	sealed  bool
}

func NewRegistry() *Registry { return &Registry{modules: make(map[string]Module)} }

func (registry *Registry) Register(module Module) error {
	if module == nil {
		return errors.New("Host inventory module is required")
	}
	descriptor := module.Descriptor()
	if err := validateDescriptor(descriptor); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return errors.New("Host inventory module registry is sealed")
	}
	if _, exists := registry.modules[descriptor.Name]; exists {
		return fmt.Errorf("Host inventory module %s is already registered", descriptor.Name)
	}
	registry.modules[descriptor.Name] = module
	return nil
}

func (registry *Registry) AdvertisedCapabilities() ([]string, error) {
	modules, err := registry.sealAndList()
	if err != nil {
		return nil, err
	}
	var capabilities []string
	for _, module := range modules {
		capabilities = append(capabilities, module.Descriptor().CapabilityNames...)
	}
	sort.Strings(capabilities)
	return capabilities, nil
}

func (registry *Registry) Collect(ctx context.Context, hostID string, generation uint64) (Snapshot, error) {
	if hostID == "" || generation == 0 {
		return Snapshot{}, errors.New("Host identity and observation generation are required")
	}
	modules, err := registry.sealAndList()
	if err != nil {
		return Snapshot{}, err
	}
	fragments := make([]Fragment, len(modules))
	group, groupContext := errgroup.WithContext(ctx)
	for index, module := range modules {
		index, module := index, module
		group.Go(func() error {
			fragment, err := module.Collect(groupContext)
			if err != nil {
				return fmt.Errorf("collect Host inventory module %s: %w", module.Descriptor().Name, err)
			}
			if err := bindDescriptor(&fragment, module.Descriptor()); err != nil {
				return err
			}
			fragments[index] = fragment
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return Snapshot{}, err
	}
	status := "COMPLETE"
	for _, fragment := range fragments {
		for _, capability := range fragment.Capabilities {
			if capability.State == AvailabilityUnknown {
				status = "DEGRADED"
			}
		}
	}
	snapshot := Snapshot{SchemaVersion: SnapshotSchemaV3, HostIdentity: hostID, ObservationGeneration: generation, CollectionStatus: status, Fragments: fragments}
	if err := snapshot.NormalizeAndValidate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (registry *Registry) sealAndList() ([]Module, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.modules) == 0 {
		return nil, errors.New("at least one Host inventory module is required")
	}
	registry.sealed = true
	modules := make([]Module, 0, len(registry.modules))
	for _, module := range registry.modules {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Descriptor().Name < modules[j].Descriptor().Name })
	return modules, nil
}

func validateDescriptor(descriptor ModuleDescriptor) error {
	if descriptor.Name == "" || descriptor.Version == "" || descriptor.SchemaVersion == "" || !validDigest(descriptor.ArtifactDigest) {
		return errors.New("complete Host inventory module descriptor is required")
	}
	if _, known := knownDomains[descriptor.Domain]; !known {
		return fmt.Errorf("unknown Host inventory module domain %s", descriptor.Domain)
	}
	if len(descriptor.CapabilityNames) == 0 {
		return errors.New("Host inventory module must advertise capabilities")
	}
	seen := make(map[string]struct{}, len(descriptor.CapabilityNames))
	for _, capability := range descriptor.CapabilityNames {
		if capability == "" {
			return errors.New("Host inventory capability name is required")
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("duplicate Host inventory capability %s", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func bindDescriptor(fragment *Fragment, descriptor ModuleDescriptor) error {
	if fragment.Domain != descriptor.Domain {
		return fmt.Errorf("Host inventory module %s returned domain %s, want %s", descriptor.Name, fragment.Domain, descriptor.Domain)
	}
	fragment.Source = Source{ModuleName: descriptor.Name, ModuleVersion: descriptor.Version, SchemaVersion: descriptor.SchemaVersion, ArtifactDigest: descriptor.ArtifactDigest}
	allowed := make(map[string]struct{}, len(descriptor.CapabilityNames))
	for _, name := range descriptor.CapabilityNames {
		allowed[name] = struct{}{}
	}
	for _, capability := range fragment.Capabilities {
		if _, ok := allowed[capability.Name]; !ok {
			return fmt.Errorf("Host inventory module %s returned undeclared capability %s", descriptor.Name, capability.Name)
		}
	}
	if len(fragment.Capabilities) != len(descriptor.CapabilityNames) {
		return fmt.Errorf("Host inventory module %s did not report every declared capability", descriptor.Name)
	}
	return nil
}
