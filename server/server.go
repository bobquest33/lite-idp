package server

import (
	"crypto/tls"
	"github.com/amdonov/lite-idp/attributes"
	"github.com/amdonov/lite-idp/authentication"
	"github.com/amdonov/lite-idp/config"
	"github.com/amdonov/lite-idp/handler"
	"github.com/amdonov/lite-idp/protocol"
	"github.com/amdonov/lite-idp/store"
	"github.com/amdonov/xmlsig"
	"log"
	"net/http"
	"os"
)

type IDP interface {
	Start() error
}

type idp struct {
	server      *http.Server
	certificate string
	key         string
}

func (idp *idp) Start() error {
	return idp.server.ListenAndServeTLS(idp.certificate, idp.key)
}

func New() (IDP, error) {
	// Load configuration data
	config, err := config.LoadConfiguration()
	if err != nil {
		return nil, err
	}
	// Create a session store
	store := store.New(config.Redis.Address)

	// Configure the XML signer
	signer, err := getSigner(config.Certificate, config.Key)
	if err != nil {
		return nil, err
	}
	// Load the JSON Attribute Store
	log.Println(config.AttributeProviders.JsonStore.File)
	people, err := os.Open(config.AttributeProviders.JsonStore.File)
	if err != nil {
		return nil, err
	}
	defer people.Close()
	retriever, err := attributes.NewJSONRetriever(people)
	if err != nil {
		return nil, err
	}
	requestParser := protocol.NewRedirectRequestParser()
	marshallers := make(map[string]protocol.ResponseMarshaller)
	marshallers["urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Artifact"] = protocol.NewArtifactResponseMarshaller(store)
	marshallers["urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"] = protocol.NewPOSTResponseMarshaller(signer)
	generator := protocol.NewDefaultGenerator(config.EntityId)
	responder := &authnresponder{retriever, generator, marshallers}
	passwordAuth := authentication.NewPasswordAuthenticator(responder.completeAuth, store, config.Authenticator.Fallback.Form)
	pkiAuth := authentication.NewPKIAuthenticator(responder.completeAuth, store, passwordAuth)
	authHandler := handler.NewAuthenticationHandler(requestParser, pkiAuth)
	http.Handle(config.Services.Authentication, authHandler)
	queryHandler := handler.NewQueryHandler(signer, retriever, config.EntityId)
	artHandler := handler.NewArtifactHandler(store, signer, config.EntityId)
	http.Handle(config.Services.ArtifactResolution, artHandler)
	http.Handle(config.Services.AttributeQuery, queryHandler)
	metadataHandler, err := handler.NewMetadataHandler(config)
	if err != nil {
		return nil, err
	}
	http.Handle(config.Services.Metadata, metadataHandler)
	form := config.Authenticator.Fallback.Form
	http.Handle(form.Context, http.StripPrefix(form.Context, http.FileServer(http.Dir(form.Directory))))
	http.Handle(form.Action, passwordAuth)
	tlsConfig := &tls.Config{ClientAuth: tls.RequestClientCert}
	// Start the server
	return &idp{&http.Server{TLSConfig: tlsConfig, Addr: config.Address}, config.Certificate, config.Key}, nil
}

func getSigner(certPath string, keyPath string) (xmlsig.Signer, error) {
	cert, err := os.Open(certPath)
	if err != nil {
		return nil, err
	}
	defer cert.Close()
	key, err := os.Open(keyPath)
	if err != nil {
		return nil, err
	}
	return xmlsig.NewSigner(key, cert)
}
