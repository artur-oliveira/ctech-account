package kyc

// MaskCPF renders a CPF as XXX.•••.•••-XX (last two digits visible).
func MaskCPF(cpf string) string {
	if len(cpf) != 11 {
		return ""
	}
	return cpf[:3] + ".•••.•••-" + cpf[9:]
}
