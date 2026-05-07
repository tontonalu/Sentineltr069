package diagnostics

import "errors"

// ErrInvalidRequest — handler deve mapear para 400 Bad Request.
var ErrInvalidRequest = errors.New("requisição inválida: host obrigatório")

// ErrUnsupportedDataModel — CPE com tree exótico (sem InternetGatewayDevice
// nem Device root). Operador deve revisar o sync ou esperar um inform.
var ErrUnsupportedDataModel = errors.New("data model do CPE não identificado (sem InternetGatewayDevice nem Device)")
