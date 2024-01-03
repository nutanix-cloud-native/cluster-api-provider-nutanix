package client

import (
	"errors"
	"testing"
)

func Test_validateAndSanitizePrismCentralInfoAddress(t *testing.T) {
	tests := []struct {
		name            string
		providedAddress string
		expectedAddress string
		expectedErr     error
	}{
		{
			"with scheme",
			"https://prism-bowser.ntnxsherlock.com",
			"prism-bowser.ntnxsherlock.com",
			nil,
		},
		{
			"with scheme and port",
			"https://prism-bowser.ntnxsherlock.com:9440",
			"prism-bowser.ntnxsherlock.com",
			nil,
		},
		{
			"as expected from the user",
			"prism-bowser.ntnxsherlock.com",
			"prism-bowser.ntnxsherlock.com",
			nil,
		},
		{
			"not set",
			"",
			"",
			ErrPrismAddressNotSet,
		},
	}

	for _, test := range tests {
		s, err := validateAndSanitizePrismCentralInfoAddress(test.providedAddress)
		if err != nil {
			if test.expectedErr == nil || !errors.Is(err, test.expectedErr) {
				t.Errorf("validateAndSanitizePrismCentralInfoAddress() error = %v, wantErr = %v", err, test.expectedErr)
			}
		}
		if s != test.expectedAddress {
			t.Errorf("validateAndSanitizePrismCentralInfoAddress() got = %v, want = %v", s, test.expectedAddress)
		}
	}
}
