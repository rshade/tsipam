package ipamsvc_test

import (
	"context"
	"testing"

	ipam "github.com/rshade/tailscale-utils/tail-ipam/gen/ipam"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/ipamsvc"
)

func TestServiceUDPPortReserveListRelease(t *testing.T) {
	t.Parallel()

	service := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	reserved, err := service.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
		Tailnet: "acme", Site: " branch ", Port: "041642", Device: " branch-fw01 ",
	})
	if err != nil {
		t.Fatalf("ReserveUDPPort: %v", err)
	}
	if reserved.Site != "branch" || reserved.Port != "41642" ||
		reserved.Device != "branch-fw01" || !reserved.Changed {
		t.Errorf("ReserveUDPPort = %+v, want canonical values and Changed=true", reserved)
	}

	repeated, err := service.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
		Tailnet: "acme", Site: "branch", Port: "41642", Device: "branch-fw01",
	})
	if err != nil {
		t.Fatalf("repeat ReserveUDPPort: %v", err)
	}
	if repeated.Changed {
		t.Errorf("repeat ReserveUDPPort = %+v, want Changed=false", repeated)
	}

	if _, crossSiteErr := service.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
		Tailnet: "acme", Site: "hq", Port: "41642", Device: "hq-fw01",
	}); crossSiteErr != nil {
		t.Fatalf("cross-site ReserveUDPPort: %v", crossSiteErr)
	}

	all, err := service.ListUDPPorts(ctx, &ipam.ListUDPPortsPayload{Tailnet: "acme"})
	if err != nil {
		t.Fatalf("ListUDPPorts: %v", err)
	}
	if len(all) != 2 || all[0].Site != "branch" || all[1].Site != "hq" {
		t.Errorf("ListUDPPorts = %+v, want branch then hq", all)
	}

	filter := " hq "
	filtered, err := service.ListUDPPorts(ctx, &ipam.ListUDPPortsPayload{Tailnet: "acme", Site: &filter})
	if err != nil {
		t.Fatalf("filtered ListUDPPorts: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Site != "hq" || filtered[0].Device != "hq-fw01" {
		t.Errorf("filtered ListUDPPorts = %+v, want one hq reservation", filtered)
	}

	released, err := service.ReleaseUDPPort(ctx, &ipam.ReleaseUDPPortPayload{
		Tailnet: "acme", Site: " branch ", Port: "041642",
	})
	if err != nil {
		t.Fatalf("ReleaseUDPPort: %v", err)
	}
	if released.Site != "branch" || released.Port != "41642" || !released.Changed {
		t.Errorf("ReleaseUDPPort = %+v, want canonical values and Changed=true", released)
	}
}

func TestServiceUDPPortCollisionReturnsNamedError(t *testing.T) {
	t.Parallel()

	service := &ipamsvc.Service{RegistryDir: t.TempDir()}
	ctx := context.Background()
	if _, err := service.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
		Tailnet: "acme", Site: "branch", Port: "41642", Device: "branch-fw01",
	}); err != nil {
		t.Fatalf("seeding reservation: %v", err)
	}
	_, err := service.ReserveUDPPort(ctx, &ipam.ReserveUDPPortPayload{
		Tailnet: "acme", Site: "branch", Port: "41642", Device: "branch-fw02",
	})
	assertServiceErrorName(t, err, "collision")
}

func TestServiceUDPPortValidationReturnsNamedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*ipamsvc.Service) error
	}{
		{
			name: "reserve site",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveUDPPort(context.Background(), &ipam.ReserveUDPPortPayload{
					Tailnet: "acme", Site: " ", Port: "41642", Device: "fw",
				})
				return err
			},
		},
		{
			name: "reserve port",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveUDPPort(context.Background(), &ipam.ReserveUDPPortPayload{
					Tailnet: "acme", Site: "branch", Port: "1023", Device: "fw",
				})
				return err
			},
		},
		{
			name: "reserve device",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReserveUDPPort(context.Background(), &ipam.ReserveUDPPortPayload{
					Tailnet: "acme", Site: "branch", Port: "41642", Device: " ",
				})
				return err
			},
		},
		{
			name: "release port",
			run: func(service *ipamsvc.Service) error {
				_, err := service.ReleaseUDPPort(context.Background(), &ipam.ReleaseUDPPortPayload{
					Tailnet: "acme", Site: "branch", Port: "not-a-port",
				})
				return err
			},
		},
		{
			name: "list whitespace site",
			run: func(service *ipamsvc.Service) error {
				filter := " "
				_, err := service.ListUDPPorts(context.Background(), &ipam.ListUDPPortsPayload{
					Tailnet: "acme", Site: &filter,
				})
				return err
			},
		},
		{
			name: "list empty site",
			run: func(service *ipamsvc.Service) error {
				filter := ""
				_, err := service.ListUDPPorts(context.Background(), &ipam.ListUDPPortsPayload{
					Tailnet: "acme", Site: &filter,
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertServiceErrorName(t, test.run(&ipamsvc.Service{RegistryDir: t.TempDir()}), "invalid_input")
		})
	}
}
