package inventory

import "errors"

var (
	ErrPOPNotFound      = errors.New("inventory: POP não encontrado")
	ErrVendorNotFound   = errors.New("inventory: vendor não encontrado")
	ErrModelNotFound    = errors.New("inventory: modelo não encontrado")
	ErrCustomerNotFound = errors.New("inventory: cliente não encontrado")
	ErrDeviceNotFound   = errors.New("inventory: device não encontrado")

	ErrPPPoEDuplicate = errors.New("inventory: PPPoE já em uso por outro cliente")
	ErrSlugDuplicate  = errors.New("inventory: slug já cadastrado")
	ErrModelDuplicate = errors.New("inventory: modelo já cadastrado para este vendor")
)
