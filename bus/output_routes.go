package bus

import (
	"net/http"
	"strconv"
)

func (s *Server) prepareOutputCORS(response http.ResponseWriter, request *http.Request) error {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	response.Header().Add("Vary", "Origin")
	allowed := false
	for _, candidate := range s.options.AllowedOrigins {
		if origin == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return Errorf(CodePermissionDenied, "Browser origin is not allowed")
	}
	response.Header().Set("Access-Control-Allow-Origin", origin)
	response.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	response.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	return nil
}

func (s *Server) outputOptions(response http.ResponseWriter, request *http.Request) error {
	if err := s.prepareOutputCORS(response, request); err != nil {
		return err
	}
	response.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) createOutputStream(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input CreateOutputStreamInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	stream, err := s.runtime.CreateOutputStream(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, stream)
	return nil
}

func (s *Server) listOutputStreams(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	streams, err := s.runtime.ListOutputStreams(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, streams)
	return nil
}

func (s *Server) getOutputStream(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	stream, err := s.runtime.OutputStream(request.Context(), token, request.PathValue("streamId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, stream)
	return nil
}

func (s *Server) removeOutputStream(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	if err := s.runtime.RemoveOutputStream(request.Context(), token, request.PathValue("streamId")); err != nil {
		return err
	}
	writeResult(response, http.StatusOK, map[string]bool{"removed": true})
	return nil
}

func (s *Server) setOutputPublisher(response http.ResponseWriter, request *http.Request, allowed bool) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	stream, err := s.runtime.SetOutputPublisher(request.Context(), token, request.PathValue("streamId"), request.PathValue("agentId"), allowed)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, stream)
	return nil
}

func (s *Server) addOutputPublisher(response http.ResponseWriter, request *http.Request) error {
	return s.setOutputPublisher(response, request, true)
}

func (s *Server) removeOutputPublisher(response http.ResponseWriter, request *http.Request) error {
	return s.setOutputPublisher(response, request, false)
}

func (s *Server) createOutputPrincipal(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input CreateOutputPrincipalInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	principal, err := s.runtime.CreateOutputPrincipal(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, principal)
	return nil
}

func (s *Server) listOutputPrincipals(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	principals, err := s.runtime.ListOutputPrincipals(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, principals)
	return nil
}

func (s *Server) rotateOutputPrincipal(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input emptyInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	principal, err := s.runtime.RotateOutputPrincipal(request.Context(), token, request.PathValue("principalId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, principal)
	return nil
}

func (s *Server) setOutputPrincipal(response http.ResponseWriter, request *http.Request, enabled bool) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input emptyInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	principal, err := s.runtime.SetOutputPrincipalEnabled(request.Context(), token, request.PathValue("principalId"), enabled)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, principal)
	return nil
}

func (s *Server) enableOutputPrincipal(response http.ResponseWriter, request *http.Request) error {
	return s.setOutputPrincipal(response, request, true)
}

func (s *Server) disableOutputPrincipal(response http.ResponseWriter, request *http.Request) error {
	return s.setOutputPrincipal(response, request, false)
}

func (s *Server) publishOutput(response http.ResponseWriter, request *http.Request) error {
	if err := s.prepareOutputCORS(response, request); err != nil {
		return err
	}
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input PublishOutputInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	value, err := s.runtime.PublishOutput(request.Context(), token, request.PathValue("streamId"), input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, value)
	return nil
}

func (s *Server) latestOutput(response http.ResponseWriter, request *http.Request) error {
	if err := s.prepareOutputCORS(response, request); err != nil {
		return err
	}
	token, err := bearer(request)
	if err != nil {
		return err
	}
	value, err := s.runtime.LatestOutput(request.Context(), token, request.PathValue("streamId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, value)
	return nil
}

func (s *Server) outputHistory(response http.ResponseWriter, request *http.Request) error {
	if err := s.prepareOutputCORS(response, request); err != nil {
		return err
	}
	token, err := bearer(request)
	if err != nil {
		return err
	}
	after, err := queryInteger(request, "after", 0)
	if err != nil {
		return err
	}
	limit64, err := queryInteger(request, "limit", defaultOutputLimit)
	if err != nil {
		return err
	}
	if limit64 > int64(^uint(0)>>1) {
		return Errorf(CodeInvalidArgument, "limit is invalid")
	}
	history, err := s.runtime.OutputHistory(request.Context(), token, request.PathValue("streamId"), after, int(limit64))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, history)
	return nil
}

func queryInteger(request *http.Request, name string, fallback int64) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, Errorf(CodeInvalidArgument, name+" must be an integer")
	}
	return parsed, nil
}
