package verify

import "github.com/joshduffy/readback/schemas"

const ClaimsSchemaVersion = 1

func ClaimsSchemaJSON() []byte { return schemas.Claims() }
func ResultSchemaJSON() []byte { return schemas.Result() }
