package model

import (
	"foundry/internal/domain"
	"net/http"
)

type routedHTTPResponse struct {
	*http.Response
	transport domain.HTTPTransportDiagnostic
	exchange  domain.HTTPExchangeDiagnostic
}

// This observes net/http's parsed response, not the raw wire. In particular,
// ContentLength may be -1 after decompression; it is not a body-read counter.
func observeHTTPTransport(response *http.Response) domain.HTTPTransportDiagnostic {
	d := domain.HTTPTransportDiagnostic{Version: 1, ContentType: "absent", Protocol: "other", BodyLength: "unknown"}
	values := response.Header.Values("Content-Type")
	switch {
	case len(values) > 1:
		d.ContentType = "multiple"
	case len(values) == 1 && values[0] == "":
		d.ContentType = "empty"
	case len(values) == 1:
		d.ContentType = "single"
	}
	switch {
	case response.ProtoMajor == 1 && response.ProtoMinor == 0:
		d.Protocol = "http1_0"
	case response.ProtoMajor == 1 && response.ProtoMinor == 1:
		d.Protocol = "http1_1"
	case response.ProtoMajor == 2 && response.ProtoMinor == 0:
		d.Protocol = "http2"
	}
	switch {
	case response.ContentLength == 0:
		d.BodyLength = "zero"
	case response.ContentLength > 0:
		d.BodyLength = "positive"
	}
	return d
}
