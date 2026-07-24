package mobileconfig

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

var (
	oidData            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidContentType     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSHA256          = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA256WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
)

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue `asn1:"set"`
}

type issuerAndSerialNumber struct {
	Issuer       asn1.RawValue
	SerialNumber *big.Int
}

type signerInfo struct {
	Version            int
	SID                issuerAndSerialNumber
	DigestAlgorithm    algorithmIdentifier
	SignedAttrs        asn1.RawValue
	SignatureAlgorithm algorithmIdentifier
	Signature          []byte
}

type encapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     asn1.RawValue `asn1:"optional"`
}

type signedData struct {
	Version          int
	DigestAlgorithms []algorithmIdentifier `asn1:"set"`
	ContentInfo      encapContentInfo
	Certificates     asn1.RawValue
	SignerInfos      []signerInfo `asn1:"set"`
}

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue
}

type Signer struct {
	certificate *x509.Certificate
	privateKey  crypto.Signer
}

func LoadSigner(certificatePath, privateKeyPath string) (*Signer, error) {
	if certificatePath == "" || privateKeyPath == "" {
		return nil, errors.New("profile certificate and private key are required")
	}
	certPEM, err := os.ReadFile(certificatePath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("profile certificate is empty")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("profile private key cannot sign")
	}
	if !publicKeysEqual(cert.PublicKey, key.Public()) {
		return nil, errors.New("profile certificate and private key do not match")
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, errors.New("profile signing certificate is not currently valid")
	}
	return &Signer{certificate: cert, privateKey: key}, nil
}

func (s *Signer) Sign(content []byte) ([]byte, error) {
	digest := sha256.Sum256(content)
	oidDER, _ := asn1.Marshal(oidData)
	digestDER, _ := asn1.Marshal(digest[:])
	attrs := []attribute{
		{Type: oidContentType, Values: []asn1.RawValue{{FullBytes: oidDER}}},
		{Type: oidMessageDigest, Values: []asn1.RawValue{{FullBytes: digestDER}}},
	}
	signedAttrsDER, err := asn1.MarshalWithParams(attrs, "set")
	if err != nil {
		return nil, err
	}
	attrsDigest := sha256.Sum256(signedAttrsDER)
	signature, err := s.privateKey.Sign(rand.Reader, attrsDigest[:], crypto.SHA256)
	if err != nil {
		return nil, err
	}
	signatureAlgorithm := algorithmIdentifier{}
	switch s.privateKey.(type) {
	case *rsa.PrivateKey:
		signatureAlgorithm = algorithmIdentifier{Algorithm: oidSHA256WithRSA, Parameters: asn1.RawValue{Tag: 5}}
	case *ecdsa.PrivateKey:
		signatureAlgorithm = algorithmIdentifier{Algorithm: oidECDSAWithSHA256}
	default:
		return nil, fmt.Errorf("unsupported profile signing key %T", s.privateKey)
	}
	signedAttrsTagged := append([]byte(nil), signedAttrsDER...)
	signedAttrsTagged[0] = 0xa0
	octets, _ := asn1.Marshal(content)
	data := signedData{
		Version:          1,
		DigestAlgorithms: []algorithmIdentifier{{Algorithm: oidSHA256, Parameters: asn1.RawValue{Tag: 5}}},
		ContentInfo:      encapContentInfo{EContentType: oidData, EContent: asn1.RawValue{FullBytes: wrap(0xa0, octets)}},
		Certificates:     asn1.RawValue{FullBytes: wrap(0xa0, s.certificate.Raw)},
		SignerInfos: []signerInfo{{
			Version: 1, SID: issuerAndSerialNumber{Issuer: asn1.RawValue{FullBytes: s.certificate.RawIssuer}, SerialNumber: s.certificate.SerialNumber},
			DigestAlgorithm: algorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.RawValue{Tag: 5}},
			SignedAttrs:     asn1.RawValue{FullBytes: signedAttrsTagged}, SignatureAlgorithm: signatureAlgorithm, Signature: signature,
		}},
	}
	signedDataDER, err := asn1.Marshal(data)
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(contentInfo{ContentType: oidSignedData, Content: asn1.RawValue{FullBytes: wrap(0xa0, signedDataDER)}})
}

func wrap(tag byte, content []byte) []byte {
	length := len(content)
	encoded := []byte{tag}
	if length < 128 {
		encoded = append(encoded, byte(length))
	} else {
		bytes := make([]byte, 0, 4)
		for value := length; value > 0; value >>= 8 {
			bytes = append([]byte{byte(value)}, bytes...)
		}
		encoded = append(encoded, 0x80|byte(len(bytes)))
		encoded = append(encoded, bytes...)
	}
	return append(encoded, content...)
}

func publicKeysEqual(left, right any) bool {
	a, err := x509.MarshalPKIXPublicKey(left)
	if err != nil {
		return false
	}
	b, err := x509.MarshalPKIXPublicKey(right)
	if err != nil {
		return false
	}
	return string(a) == string(b)
}

func PEMCertificate(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}
