package hsql

import (
	"crypto/tls"
	"fmt"
	"sync"
)

var (
	tlsConfigsMu sync.RWMutex
	tlsConfigs   = map[string]*tls.Config{}
)

// RegisterTLSConfig registers a named TLS configuration for hsqls:// DSNs.
// Reference it as hsqls://host/db?tlsconfig=name. The config is cloned when a
// connection is opened, so later mutations to cfg are not applied retroactively.
func RegisterTLSConfig(name string, cfg *tls.Config) error {
	if name == "" {
		return fmt.Errorf("hsql: TLS config name is empty")
	}
	if cfg == nil {
		return fmt.Errorf("hsql: TLS config %q is nil", name)
	}
	tlsConfigsMu.Lock()
	defer tlsConfigsMu.Unlock()
	tlsConfigs[name] = cfg.Clone()
	return nil
}

// DeregisterTLSConfig removes a registered TLS configuration.
func DeregisterTLSConfig(name string) {
	tlsConfigsMu.Lock()
	defer tlsConfigsMu.Unlock()
	delete(tlsConfigs, name)
}

func lookupTLSConfig(name string) (*tls.Config, bool) {
	tlsConfigsMu.RLock()
	defer tlsConfigsMu.RUnlock()
	cfg, ok := tlsConfigs[name]
	if !ok {
		return nil, false
	}
	return cfg.Clone(), true
}
