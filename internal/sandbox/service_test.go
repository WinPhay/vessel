package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/WinPhay/vessel/internal/tokenfilter"
)

func TestServiceManager_Lifecycle(t *testing.T) {
	sm, err := NewServiceManager(nil, "", "test-session")
	if err != nil {
		t.Fatalf("failed to create ServiceManager: %v", err)
	}

	ctx := context.Background()

	// 1. Spawn valid service
	info, err := sm.SpawnService(ctx, "redis", "redis:7-alpine", nil, "")
	if err != nil {
		t.Fatalf("failed to spawn redis service: %v", err)
	}
	if info.Name != "redis" || info.Hostname != "redis" || info.Status != "running" {
		t.Errorf("unexpected service info: %+v", info)
	}

	// 2. Reject duplicate service
	_, err = sm.SpawnService(ctx, "redis", "redis:7-alpine", nil, "")
	if err == nil {
		t.Errorf("expected error when spawning duplicate service 'redis'")
	}

	// 3. Reject invalid names
	invalidNames := []string{
		"-starts-with-hyphen",
		"ends-with-hyphen-",
		"invalid/slash",
		"invalid space",
		"",
	}
	for _, bad := range invalidNames {
		_, err = sm.SpawnService(ctx, bad, "redis:7-alpine", nil, "")
		if err == nil {
			t.Errorf("expected error for invalid service name %q", bad)
		}
	}

	// 4. List services
	services, err := sm.ListServices(ctx)
	if err != nil {
		t.Fatalf("failed to list services: %v", err)
	}
	if len(services) != 1 || services[0].Name != "redis" {
		t.Errorf("expected 1 service 'redis', got: %d", len(services))
	}

	// 5. Stop service
	if err := sm.StopService(ctx, "redis"); err != nil {
		t.Fatalf("failed to stop service: %v", err)
	}

	// 6. Stop non-existent service
	if err := sm.StopService(ctx, "redis"); err == nil {
		t.Errorf("expected error stopping non-existent service")
	}

	// 7. Verify list is empty
	services, _ = sm.ListServices(ctx)
	if len(services) != 0 {
		t.Errorf("expected 0 services, got: %d", len(services))
	}

	// 8. Close cleanup
	if err := sm.Close(ctx); err != nil {
		t.Errorf("close error: %v", err)
	}
}

func TestServiceManager_DLPSecretRegistration(t *testing.T) {
	sm, _ := NewServiceManager(nil, "", "dlp-session")
	ctx := context.Background()

	secretPass := "SuperSecretPostgresPassword9988!"
	env := map[string]string{
		"POSTGRES_USER":     "admin",
		"POSTGRES_PASSWORD": secretPass,
	}

	_, err := sm.SpawnService(ctx, "postgres", "postgres:15-alpine", env, "")
	if err != nil {
		t.Fatalf("failed to spawn service: %v", err)
	}

	// Check if secret was automatically registered into tokenfilter DLP vault
	output := tokenfilter.CleanOutput("Connecting with password: " + secretPass)
	if strings.Contains(output, secretPass) {
		t.Errorf("service password leaked in output")
	}
	if !strings.Contains(output, "[REDACTED:POSTGRES_PASSWORD]") {
		t.Errorf("expected password to be redacted as POSTGRES_PASSWORD, got: %s", output)
	}
}

func TestServiceMesh_PersonaQA(t *testing.T) {
	ctx := context.Background()

	// =========================================================================
	// TIER 1: The Monkey / Fuzz Tester
	// Rapid churn, uppercase names, long names, empty commands
	// =========================================================================
	t.Run("Tier1_Monkey_Chaos", func(t *testing.T) {
		sm, _ := NewServiceManager(nil, "", "tier1-session")
		defer func() { _ = sm.Close(ctx) }()

		// Uppercase names should be automatically normalized
		info, err := sm.SpawnService(ctx, "MY-DATABASE", "alpine:latest", nil, "")
		if err != nil {
			t.Fatalf("Tier 1: failed to spawn service with uppercase name: %v", err)
		}
		if info.Name != "my-database" {
			t.Errorf("Tier 1: expected lowercase normalized name, got: %s", info.Name)
		}

		// Empty image must fail
		_, err = sm.SpawnService(ctx, "valid-name", "", nil, "")
		if err == nil {
			t.Errorf("Tier 1: expected error on empty image")
		}

		// Rapid churn: spawn and stop 20 services in sequence
		for i := 0; i < 20; i++ {
			sName := "churn-svc"
			_, err := sm.SpawnService(ctx, sName, "alpine:latest", nil, "")
			if err != nil {
				t.Fatalf("Tier 1: churn spawn failed at %d: %v", i, err)
			}
			if err := sm.StopService(ctx, sName); err != nil {
				t.Fatalf("Tier 1: churn stop failed at %d: %v", i, err)
			}
		}
	})

	// =========================================================================
	// TIER 2: The Blind Box / Accidental Developer
	// Realistic full-stack microservices (db, cache, queue)
	// =========================================================================
	t.Run("Tier2_BlindBox_FullStackMesh", func(t *testing.T) {
		sm, _ := NewServiceManager(nil, "", "tier2-session")
		defer func() { _ = sm.Close(ctx) }()

		stack := []struct {
			name  string
			image string
			env   map[string]string
		}{
			{"postgres", "postgres:15-alpine", map[string]string{"POSTGRES_PASSWORD": "devpassword"}},
			{"redis", "redis:7-alpine", nil},
			{"rabbitmq", "rabbitmq:3-alpine", map[string]string{"RABBITMQ_DEFAULT_PASS": "guest"}},
		}

		for _, s := range stack {
			info, err := sm.SpawnService(ctx, s.name, s.image, s.env, "")
			if err != nil {
				t.Fatalf("Tier 2: failed to spawn %s: %v", s.name, err)
			}
			if info.Hostname != s.name {
				t.Errorf("Tier 2: expected hostname %s, got: %s", s.name, info.Hostname)
			}
		}

		list, err := sm.ListServices(ctx)
		if err != nil {
			t.Fatalf("Tier 2: failed to list: %v", err)
		}
		if len(list) != 3 {
			t.Errorf("Tier 2: expected 3 running services, got: %d", len(list))
		}
	})

	// =========================================================================
	// TIER 3: The Semi-Tech Agent
	// Listing when empty, stopping non-existent, calling on closed manager
	// =========================================================================
	t.Run("Tier3_SemiTech_EdgeCases", func(t *testing.T) {
		sm, _ := NewServiceManager(nil, "", "tier3-session")

		// List on empty mesh returns empty slice, not nil
		list, err := sm.ListServices(ctx)
		if err != nil || list == nil {
			t.Errorf("Tier 3: expected non-nil slice on empty mesh")
		}

		// Stop non-existent
		err = sm.StopService(ctx, "ghost-service")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("Tier 3: expected not found error, got: %v", err)
		}

		// Close manager
		_ = sm.Close(ctx)

		// Subsequent calls must fail with ErrSandboxClosed
		_, err = sm.SpawnService(ctx, "new-svc", "alpine", nil, "")
		if err != ErrSandboxClosed {
			t.Errorf("Tier 3: expected ErrSandboxClosed, got: %v", err)
		}
	})

	// =========================================================================
	// TIER 4: The Adversarial Adversary
	// Deliberate attempts to exploit sidecars: path traversal in names,
	// DNS spoofing names, credential leaking in environment
	// =========================================================================
	t.Run("Tier4_Adversarial_MeshSecurity", func(t *testing.T) {
		sm, _ := NewServiceManager(nil, "", "tier4-session")
		defer func() { _ = sm.Close(ctx) }()

		// 1. Path traversal injection in service name
		traversalNames := []string{
			"../../etc/passwd",
			"..\\windows\\system32",
			"; rm -rf /",
			"db;cat /etc/shadow",
			"`whoami`",
			"$(id)",
		}
		for _, name := range traversalNames {
			_, err := sm.SpawnService(ctx, name, "alpine", nil, "")
			if err == nil {
				t.Errorf("Tier 4: malicious service name %q was not rejected", name)
			}
		}

		// 2. Secret Redaction on egress
		jwtKey := "secret-symmetric-jwt-key-xyz123456789"
		_, err := sm.SpawnService(ctx, "auth-svc", "alpine", map[string]string{
			"AUTH_SECRET_KEY": jwtKey,
		}, "")
		if err != nil {
			t.Fatalf("Tier 4: spawn failed: %v", err)
		}

		testLog := "Server started with AUTH_SECRET_KEY=" + jwtKey
		cleaned := tokenfilter.CleanOutput(testLog)
		if strings.Contains(cleaned, jwtKey) {
			t.Errorf("Tier 4: secret injected in service leaked in output")
		}
		if !strings.Contains(cleaned, "[REDACTED:AUTH_SECRET_KEY]") {
			t.Errorf("Tier 4: expected AUTH_SECRET_KEY redaction badge, got: %s", cleaned)
		}
	})
}

func TestServiceManager_Persistence(t *testing.T) {
	sm, err := NewServiceManager(nil, "", "persist-mesh-session")
	if err != nil {
		t.Fatalf("failed to create ServiceManager: %v", err)
	}

	sm.SetPersistent(true)
	if !sm.IsPersistent() {
		t.Errorf("expected persistent to be true")
	}

	ctx := context.Background()
	_, err = sm.SpawnService(ctx, "cache", "redis:7-alpine", nil, "")
	if err != nil {
		t.Fatalf("failed to spawn service: %v", err)
	}

	// Close with persistent=true must preserve services in memory
	if err := sm.Close(ctx); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	svcs, err := sm.ListServices(ctx)
	if err != nil {
		t.Fatalf("list services failed: %v", err)
	}
	if len(svcs) != 1 || svcs[0].Name != "cache" {
		t.Errorf("expected 1 persistent service 'cache', got: %d", len(svcs))
	}
}

func TestParsePortSpecs(t *testing.T) {
	// 1. Valid specs
	validSpecs := []string{
		"5432:5432",
		"127.0.0.1:8080:80",
		"localhost:3000:3000",
		"6379",
		"9000:9000/udp",
	}
	portSet, portMap, descs, err := ParsePortSpecs(validSpecs)
	if err != nil {
		t.Fatalf("unexpected error parsing valid specs: %v", err)
	}
	if len(portSet) != 5 || len(portMap) != 5 || len(descs) != 5 {
		t.Errorf("expected 5 parsed specs, got set=%d map=%d descs=%d", len(portSet), len(portMap), len(descs))
	}

	// 2. Security Invariant: reject non-localhost bindings (0.0.0.0, external IPs)
	insecureSpecs := []string{
		"0.0.0.0:5432:5432",
		"192.168.1.10:5432:5432",
		"10.0.0.1:80:80",
	}
	for _, spec := range insecureSpecs {
		_, _, _, err := ParsePortSpecs([]string{spec})
		if err == nil {
			t.Errorf("expected error for insecure binding %q, got nil", spec)
		}
		if !strings.Contains(err.Error(), "insecure port binding") {
			t.Errorf("expected insecure port binding error message, got: %v", err)
		}
	}

	// 3. Reject invalid port numbers
	invalidSpecs := []string{
		"70000:5432",
		"5432:70000",
		"0:5432",
		"-10:5432",
		"invalid:5432",
		"5432:invalid",
		"5432:5432/invalidproto",
	}
	for _, spec := range invalidSpecs {
		_, _, _, err := ParsePortSpecs([]string{spec})
		if err == nil {
			t.Errorf("expected error for invalid spec %q, got nil", spec)
		}
	}
}

func TestServiceManager_PortMapping(t *testing.T) {
	sm, err := NewServiceManager(nil, "", "port-mesh-session")
	if err != nil {
		t.Fatalf("failed to create ServiceManager: %v", err)
	}
	ctx := context.Background()

	// Spawn service with valid port mappings
	info, err := sm.SpawnService(ctx, "postgres", "postgres:16-alpine", nil, "", "5432:5432", "127.0.0.1:8080:80")
	if err != nil {
		t.Fatalf("failed to spawn service with ports: %v", err)
	}
	if len(info.Ports) != 2 {
		t.Fatalf("expected 2 port mappings in ServiceInfo, got: %d", len(info.Ports))
	}
	if !strings.Contains(info.Ports[0], "5432") {
		t.Errorf("expected port description to contain 5432, got: %s", info.Ports[0])
	}

	// Reject insecure port mapping on spawn
	_, err = sm.SpawnService(ctx, "insecure-svc", "alpine", nil, "", "0.0.0.0:9000:9000")
	if err == nil {
		t.Fatalf("expected error when spawning service with 0.0.0.0 port mapping")
	}
}
