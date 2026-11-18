package imageoptions

import _ "embed"

// JSONSchema is docs/contracts/schemas/image-options.schema.json, generated
// from compute-types. `task gen:types` refreshes this copy (go:embed cannot
// reach outside the module) and TestJSONSchemaIsCurrent checks it. The MCP
// server publishes it as the schema of the options tool arguments.
//
//go:embed image-options.schema.json
var JSONSchema []byte
