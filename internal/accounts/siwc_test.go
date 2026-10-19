package accounts

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/model"
)

func retainedSIWCHandoffSocket(t *testing.T, flow SIWCFlow) net.Conn {
	t.Helper()
	handoff, err := url.Parse(flow.HandoffURL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp4", handoff.Host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", handoff.RequestURI(), handoff.Host); err != nil {
		connection.Close()
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		connection.Close()
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		connection.Close()
		t.Fatalf("handoff status=%d", response.StatusCode)
	}
	return connection
}

func requireSIWCSocketClosed(t *testing.T, connection net.Conn) {
	t.Helper()
	defer connection.Close()
	if err := connection.SetReadDeadline(time.Now().Add(750 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	_, err := connection.Read(one[:])
	if err == nil {
		t.Fatal("terminal SIWC listener kept an accepted connection usable")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("terminal SIWC listener left accepted keep-alive connection open")
	}
}

func newSIWCAccount(t *testing.T, service *Service, baseURL string) domain.ModelAccount {
	t.Helper()
	a, err := service.Save(domain.ModelAccount{
		Name:        "Synthetic SIWC",
		Provider:    "openai",
		BaseURL:     baseURL,
		AuthMethod:  "openai_siwc",
		BillingMode: "subscription",
		Enabled:     true,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSIWCProductionGatePrecedesHostSecretAndNetworkWork(t *testing.T) {
	service, _, secrets, path := setupAccounts(t)
	account := newSIWCAccount(t, service, "https://api.openai.com/v1")
	siwc := NewSIWCService(service)
	if siwc.SimulationEnabled() {
		t.Fatal("production service reports simulation")
	}
	_, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	var provider *model.ProviderError
	if !errors.As(err, &provider) || provider.Code != "live_provider_access_pending_confirmation" || provider.Certainty != "not_sent" {
		t.Fatalf("unsafe production Begin result: %v", err)
	}
	if secrets.gets != 0 || secrets.sets != 0 || secrets.deletes != 0 {
		t.Fatal("production gate touched secrets")
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, readErr := os.ReadFile(path + suffix)
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if strings.Contains(string(raw), "openai_siwc_host_id") {
			t.Fatal("production gate persisted host metadata")
		}
	}
	_, err = siwc.Refresh(context.Background(), "missing", account.Revision)
	if !errors.As(err, &provider) || provider.Code != "live_provider_access_pending_confirmation" {
		t.Fatalf("unsafe production Refresh result: %v", err)
	}
}

func TestSIWCSimulationRequiresEveryExactLiteralLoopbackOrigin(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	valid := SIWCOptions{
		AuthorizationOrigin: server.URL,
		TokenOrigin:         server.URL,
		OIDCOrigin:          server.URL,
		JWKSOrigin:          server.URL,
		RevocationOrigin:    server.URL,
		Issuer:              server.URL,
		FlowTimeout:         time.Minute,
	}
	serviceUnderTest, err := NewSIWCSimulation(service, valid)
	if err != nil || !serviceUnderTest.SimulationEnabled() {
		t.Fatalf("valid simulation rejected: %v", err)
	}

	bad := valid
	bad.TokenOrigin = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	if _, err = NewSIWCSimulation(service, bad); err == nil {
		t.Fatal("localhost simulation origin accepted")
	}
	bad = valid
	bad.JWKSOrigin += "/path"
	if _, err = NewSIWCSimulation(service, bad); err == nil {
		t.Fatal("origin with path accepted")
	}
	bad = valid
	bad.Issuer = "https://auth.openai.com"
	if _, err = NewSIWCSimulation(service, bad); err == nil {
		t.Fatal("non-loopback simulation issuer accepted")
	}
}

func TestSIWCFlowJSONContainsOnlySafeCoordinationMetadata(t *testing.T) {
	flow := SIWCFlow{OperationID: "operation", State: "waiting", HandoffURL: "http://127.0.0.1:1234/auth/start", ExpiresAt: "2099-01-01T00:00:00Z"}
	raw, err := json.Marshal(flow)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"operation_id":"operation","state":"waiting","handoff_url":"http://127.0.0.1:1234/auth/start","expires_at":"2099-01-01T00:00:00Z"}` {
		t.Fatalf("unexpected flow DTO: %s", raw)
	}
}

func TestSIWCBeginLifetimeIsDetachedFromRequestAndExplicitlyCancelable(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, err := NewSIWCSimulation(service, issuer.options())
	if err != nil {
		t.Fatal(err)
	}
	requestContext, requestDone := context.WithCancel(context.Background())
	flow, err := siwc.Begin(requestContext, account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	requestDone()
	if query := openHandoff(t, flow); query.Get("state") == "" {
		t.Fatal("request completion canceled the user authorization lifetime")
	}
	canceled, err := siwc.Cancel(context.Background(), flow.OperationID)
	if err != nil || canceled.State != "canceled" || canceled.HandoffURL != "" {
		t.Fatalf("cancel: %+v %v", canceled, err)
	}
	status, err := siwc.Status(flow.OperationID)
	if err != nil || status != canceled {
		t.Fatalf("cancel status: %+v %v", status, err)
	}
}

func TestSIWCCloseCancelsAllActiveListeners(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	siwc, _ := NewSIWCSimulation(service, issuer.options())
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = siwc.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := siwc.Status(flow.OperationID)
	if err != nil || status.State != "canceled" || status.HandoffURL != "" {
		t.Fatalf("close status: %+v %v", status, err)
	}
}

func TestSIWCFlowExpiresAndClosesHandoff(t *testing.T) {
	service, _, _, _ := setupAccounts(t)
	resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer resource.Close()
	account := newSIWCAccount(t, service, resource.URL+"/v1")
	issuer := newSIWCIssuerFixture(t, account.BaseURL)
	options := issuer.options()
	options.FlowTimeout = 20 * time.Millisecond
	siwc, err := NewSIWCSimulation(service, options)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, statusErr := siwc.Status(flow.OperationID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if status.State == "expired" {
			if status.HandoffURL != "" {
				t.Fatal("expired flow retained handoff URL")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("flow did not expire: %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSIWCTerminalControlPathsCloseAcceptedKeepAliveSockets(t *testing.T) {
	for _, terminal := range []string{"cancel", "close", "expiry"} {
		t.Run(terminal, func(t *testing.T) {
			service, _, _, _ := setupAccounts(t)
			resource := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer resource.Close()
			account := newSIWCAccount(t, service, resource.URL+"/v1")
			issuer := newSIWCIssuerFixture(t, account.BaseURL)
			options := issuer.options()
			if terminal == "expiry" {
				options.FlowTimeout = 50 * time.Millisecond
			}
			siwc, _ := NewSIWCSimulation(service, options)
			flow, err := siwc.Begin(context.Background(), account.ID, account.Revision)
			if err != nil {
				t.Fatal(err)
			}
			connection := retainedSIWCHandoffSocket(t, flow)
			switch terminal {
			case "cancel":
				_, err = siwc.Cancel(context.Background(), flow.OperationID)
			case "close":
				err = siwc.Close()
			case "expiry":
				deadline := time.Now().Add(time.Second)
				for {
					status, statusErr := siwc.Status(flow.OperationID)
					if statusErr != nil {
						t.Fatal(statusErr)
					}
					if status.State == "expired" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("flow did not expire")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			if err != nil {
				connection.Close()
				t.Fatal(err)
			}
			requireSIWCSocketClosed(t, connection)
		})
	}
}
