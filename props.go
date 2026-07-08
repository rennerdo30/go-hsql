package hsql

import "strings"

// parseServerProperties parses the client-properties string sent in the
// CONNECTACKNOWLEDGE response. HSQLDB encodes it as "key=value;key=value"
// (org.hsqldb.lib.HsqlProperties.delimitedArgPairsToProps with "=" / ";").
func parseServerProperties(s string) map[string]string {
	props := make(map[string]string)
	if s == "" {
		return props
	}
	for _, pair := range strings.Split(s, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		if i := strings.IndexByte(pair, '='); i >= 0 {
			props[strings.TrimSpace(pair[:i])] = strings.TrimSpace(pair[i+1:])
		} else {
			props[pair] = ""
		}
	}
	return props
}

// ServerProperties returns the session properties the server reported at
// connect time (from CONNECTACKNOWLEDGE), such as the database's default schema
// and SQL-conformance settings. Accessible via database/sql Conn.Raw.
func (c *conn) ServerProperties() map[string]string {
	out := make(map[string]string, len(c.serverProps))
	for k, v := range c.serverProps {
		out[k] = v
	}
	return out
}
