package protocol

const ServicesRequestType = "tearenv-services"

type Service struct {
	Name      string `json:"name"`
	LocalPort uint32 `json:"local_port"`
}
