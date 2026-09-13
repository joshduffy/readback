package schemas

import _ "embed"

//go:embed claims.v1.json
var claims []byte

//go:embed result.v1.json
var result []byte

func Claims() []byte { return append([]byte(nil), claims...) }
func Result() []byte { return append([]byte(nil), result...) }
