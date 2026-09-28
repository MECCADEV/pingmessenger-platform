// Command db applies PingMessenger SQL migrations and development-only seeds
// through github.com/Shaik-Sirajuddin/sqlmig. The lock key stays specific to
// this product so concurrent deploys still serialize on the same advisory lock.
package main

import (
	"os"

	"github.com/Shaik-Sirajuddin/sqlmig"
)

func main() {
	os.Exit(sqlmig.Main(os.Args[1:], "pingmessenger-db-migrations"))
}
