package bus

import "net/http"

func (s *Server) createA2APrincipal(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input CreateA2APrincipalInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.CreateA2APrincipal(request.Context(), token, input)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusCreated, result)
	return nil
}

func (s *Server) listA2APrincipals(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	result, err := s.runtime.ListA2APrincipals(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) listA2APrincipalUsage(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	usage, err := s.runtime.ListA2APrincipalUsage(request.Context(), token)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, usage)
	return nil
}

func (s *Server) rotateA2APrincipal(response http.ResponseWriter, request *http.Request) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input emptyInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.RotateA2APrincipal(request.Context(), token, request.PathValue("principalId"))
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) setA2APrincipal(response http.ResponseWriter, request *http.Request, enabled bool) error {
	token, err := bearer(request)
	if err != nil {
		return err
	}
	var input emptyInput
	if err := decodeBody(response, request, &input); err != nil {
		return err
	}
	result, err := s.runtime.SetA2APrincipalEnabled(request.Context(), token, request.PathValue("principalId"), enabled)
	if err != nil {
		return err
	}
	writeResult(response, http.StatusOK, result)
	return nil
}

func (s *Server) enableA2APrincipal(response http.ResponseWriter, request *http.Request) error {
	return s.setA2APrincipal(response, request, true)
}

func (s *Server) disableA2APrincipal(response http.ResponseWriter, request *http.Request) error {
	return s.setA2APrincipal(response, request, false)
}
