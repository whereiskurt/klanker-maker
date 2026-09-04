// Command km-creds is the credential_process helper the sandbox user's
// ~/.aws/config names when spec.secrets.fenceIMDS is on.
//
// It is deliberately the dumbest component in the phase: it asks the broker for
// credentials and prints the answer verbatim. It never calls STS, never caches,
// never retries, and never touches IMDS — it CANNOT touch IMDS, because it runs
// as uid sandbox, which is exactly what the fence blocks. All of the policy lives
// in km-secretsd, which is root and unfenced.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	creds, err := fetch(secrets.SocketPath)
	if err != nil {
		// Fail closed and loudly. An empty stdout with a zero exit reaches the
		// operator as "unparseable credential_process output", which names the
		// wrong component entirely.
		fmt.Fprintf(os.Stderr, "km-creds: %v\n", err)
		fmt.Fprintln(os.Stderr, "km-creds: is km-secretsd running? try: systemctl status km-secretsd")
		os.Exit(1)
	}
	out, err := render(creds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-creds: %v\n", err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(out)
}

// fetch asks the broker for narrowed credentials.
func fetch(socketPath string) (*secrets.Credentials, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the secrets broker at %s: %w", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(secrets.UnsealRequest{Op: secrets.OpCredentials}); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	var resp secrets.UnsealResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.Credentials == nil {
		return nil, errors.New("broker returned no credentials")
	}
	return resp.Credentials, nil
}

// render serialises the credential_process response. The struct's json tags ARE
// the schema, so there is one shape and no chance of drift between what the
// broker mints and what the AWS SDKs parse.
func render(c *secrets.Credentials) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	return append(b, '\n'), nil
}
