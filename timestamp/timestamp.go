package timestamp

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/mastahyeti/cms/oid"
	"github.com/mastahyeti/cms/protocol"
)

// Request is a TimeStampReq
// 	TimeStampReq ::= SEQUENCE  {
// 		version                      INTEGER  { v1(1) },
// 		messageImprint               MessageImprint,
// 			--a hash algorithm OID and the hash value of the data to be
// 			--time-stamped
// 		reqPolicy             TSAPolicyId              OPTIONAL,
// 		nonce                 INTEGER                  OPTIONAL,
// 		certReq               BOOLEAN                  DEFAULT FALSE,
// 		extensions            [0] IMPLICIT Extensions  OPTIONAL  }
type Request struct {
	Version        int
	MessageImprint MessageImprint
	ReqPolicy      asn1.ObjectIdentifier `asn1:"optional"`
	Nonce          *big.Int              `asn1:"optional"`
	CertReq        bool                  `asn1:"optional,default:false"`
	Extensions     []pkix.Extension      `asn1:"tag:1,optional"`
}

const nonceBytes = 16

// GenerateNonce generates a new nonce for this TSR.
func (r *Request) GenerateNonce() {
	buf := make([]byte, nonceBytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	if r.Nonce == nil {
		r.Nonce = new(big.Int)
	}

	r.Nonce.SetBytes(buf[:])
}

// Response is a TimeStampResp
// 	TimeStampResp ::= SEQUENCE  {
// 		status                  PKIStatusInfo,
// 		timeStampToken          TimeStampToken     OPTIONAL  }
//
// 	TimeStampToken ::= ContentInfo
type Response struct {
	Status         PKIStatusInfo
	TimeStampToken protocol.ContentInfo `asn1:"optional"`
}

// ParseResponse parses a BER encoded TimeStampResp.
func ParseResponse(ber []byte) (Response, error) {
	var resp Response

	der, err := protocol.BER2DER(ber)
	if err != nil {
		return resp, err
	}

	rest, err := asn1.Unmarshal(der, &resp)
	if err != nil {
		return resp, err
	}
	if len(rest) > 0 {
		return resp, errors.New("unexpected trailing data")
	}

	return resp, nil
}

// PKIStatusInfo ::= SEQUENCE {
// 	status        PKIStatus,
// 	statusString  PKIFreeText     OPTIONAL,
// 	failInfo      PKIFailureInfo  OPTIONAL  }
//
// PKIStatus ::= INTEGER {
// 	granted                (0),
// 		-- when the PKIStatus contains the value zero a TimeStampToken, as
// 		requested, is present.
// 	grantedWithMods        (1),
// 		-- when the PKIStatus contains the value one a TimeStampToken,
// 		with modifications, is present.
// 	rejection              (2),
// 	waiting                (3),
// 	revocationWarning      (4),
// 		-- this message contains a warning that a revocation is
// 		-- imminent
// 	revocationNotification (5)
// 		-- notification that a revocation has occurred   }
//
// -- When the TimeStampToken is not present
// -- failInfo indicates the reason why the
// -- time-stamp request was rejected and
// -- may be one of the following values.
//
// PKIFailureInfo ::= BIT STRING {
// 	badAlg               (0),
// 		-- unrecognized or unsupported Algorithm Identifier
// 	badRequest           (2),
// 		-- transaction not permitted or supported
// 	badDataFormat        (5),
// 		-- the data submitted has the wrong format
// 	timeNotAvailable    (14),
// 		-- the TSA's time source is not available
// 	unacceptedPolicy    (15),
// 		-- the requested TSA policy is not supported by the TSA.
// 	unacceptedExtension (16),
// 		-- the requested extension is not supported by the TSA.
// 	addInfoNotAvailable (17)
// 		-- the additional information requested could not be understood
// 		-- or is not available
// 	systemFailure       (25)
// 		-- the request cannot be handled due to system failure  }
type PKIStatusInfo struct {
	Status       int
	StatusString PKIFreeText    `asn1:"optional"`
	FailInfo     asn1.BitString `asn1:"optional"`
}

// Error represents an unsuccessful PKIStatusInfo as an error.
func (si PKIStatusInfo) Error() error {
	if si.Status == 0 {
		return nil
	}

	fiStr := ""
	if si.FailInfo.BitLength > 0 {
		fibin := make([]byte, si.FailInfo.BitLength)
		for i := range fibin {
			if si.FailInfo.At(i) == 1 {
				fibin[i] = byte('1')
			} else {
				fibin[i] = byte('0')
			}
		}
		fiStr = fmt.Sprintf(" FailInfo(0b%s)", string(fibin))
	}

	statusStr := ""
	if len(si.StatusString) > 0 {
		if strs, err := si.StatusString.Strings(); err == nil {
			statusStr = fmt.Sprintf(" StatusString(%s)", strings.Join(strs, ","))
		}
	}

	return fmt.Errorf("Bad TimeStampResp: Status(%d)%s%s", si.Status, statusStr, fiStr)
}

// PKIFreeText ::= SEQUENCE SIZE (1..MAX) OF UTF8String
type PKIFreeText []asn1.RawValue

// NewPKIFreeText creates a new PKIFreeText from a []string.
func NewPKIFreeText(txts []string) (PKIFreeText, error) {
	rvs := make([]asn1.RawValue, len(txts))

	for i := range txts {
		der, err := asn1.MarshalWithParams(txts[i], "utf8")
		if err != nil {
			return nil, err
		}
		if _, err := asn1.Unmarshal(der, &rvs[i]); err != nil {
			return nil, err
		}
	}

	return PKIFreeText(rvs), nil
}

// Strings decodes the PKIFreeText into a []string.
func (ft PKIFreeText) Strings() ([]string, error) {
	strs := make([]string, len(ft))

	for i := range ft {
		if rest, err := asn1.UnmarshalWithParams(ft[i].FullBytes, &strs[i], "utf8"); err != nil {
			return nil, err
		} else if len(rest) != 0 {
			return nil, errors.New("unexpected trailing data")
		}
	}

	return strs, nil
}

// Info is a Info
// 	Info ::= SEQUENCE  {
// 	  version                      INTEGER  { v1(1) },
// 	  policy                       TSAPolicyId,
// 	  messageImprint               MessageImprint,
// 	    -- MUST have the same value as the similar field in
// 	    -- TimeStampReq
// 	  serialNumber                 INTEGER,
// 	    -- Time-Stamping users MUST be ready to accommodate integers
// 	    -- up to 160 bits.
// 	  genTime                      GeneralizedTime,
// 	  accuracy                     Accuracy                 OPTIONAL,
// 	  ordering                     BOOLEAN             DEFAULT FALSE,
// 	  nonce                        INTEGER                  OPTIONAL,
// 	    -- MUST be present if the similar field was present
// 	    -- in TimeStampReq.  In that case it MUST have the same value.
// 	  tsa                          [0] GeneralName          OPTIONAL,
// 	  extensions                   [1] IMPLICIT Extensions   OPTIONAL  }
//
// 	TSAPolicyId ::= OBJECT IDENTIFIER
type Info struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint MessageImprint
	SerialNumber   *big.Int
	GenTime        time.Time        `asn1:"generalized"`
	Accuracy       Accuracy         `asn1:"optional"`
	Ordering       bool             `asn1:"optional,default:false"`
	Nonce          *big.Int         `asn1:"optional"`
	TSA            asn1.RawValue    `asn1:"tag:0,optional"`
	Extensions     []pkix.Extension `asn1:"tag:1,optional"`
}

// ParseInfo parses an Info out of a CMS EncapsulatedContentInfo.
func ParseInfo(eci protocol.EncapsulatedContentInfo) (Info, error) {
	i := Info{}

	if !eci.EContentType.Equal(oid.TSTInfo) {
		return i, protocol.ErrWrongType
	}

	ecval, err := eci.EContentValue()
	if err != nil {
		return i, err
	}
	if ecval == nil {
		return i, errors.New("missing EContent for non data type")
	}

	if rest, err := asn1.Unmarshal(ecval, &i); err != nil {
		return i, err
	} else if len(rest) > 0 {
		return i, errors.New("unexpected trailing data")
	}

	return i, nil
}

// GenTimeMax is the latest time at which the token could have been generated
// based on the included GenTime and Accuracy attributes.
func (i *Info) GenTimeMax() time.Time {
	return i.GenTime.Add(i.Accuracy.Duration())
}

// GenTimeMin is the earliest time at which the token could have been generated
// based on the included GenTime and Accuracy attributes.
func (i *Info) GenTimeMin() time.Time {
	return i.GenTime.Add(-i.Accuracy.Duration())
}

// MessageImprint ::= SEQUENCE  {
//   hashAlgorithm                AlgorithmIdentifier,
//   hashedMessage                OCTET STRING  }
type MessageImprint struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	HashedMessage []byte
}

// NewMessageImprint creates a new MessageImprint, digesting all bytes from the
// provided reader using the specified hash.
func NewMessageImprint(hash crypto.Hash, r io.Reader) (MessageImprint, error) {
	digestAlgorithm := oid.HashToDigestAlgorithm[hash]
	if len(digestAlgorithm) == 0 {
		return MessageImprint{}, fmt.Errorf("Unsupported hash algorithm: %d", hash)
	}

	if !hash.Available() {
		return MessageImprint{}, fmt.Errorf("Hash not avaialbe: %d", hash)
	}
	h := hash.New()
	if _, err := io.Copy(h, r); err != nil {
		return MessageImprint{}, err
	}

	return MessageImprint{
		HashAlgorithm: pkix.AlgorithmIdentifier{Algorithm: digestAlgorithm},
		HashedMessage: h.Sum(nil),
	}, nil
}

// Hash gets the crypto.Hash associated with this SignerInfo's DigestAlgorithm.
// 0 is returned for unrecognized algorithms.
func (mi MessageImprint) Hash() (crypto.Hash, error) {
	algo := mi.HashAlgorithm.Algorithm.String()
	hash := oid.DigestAlgorithmToHash[algo]
	if hash == 0 {
		return 0, fmt.Errorf("unknown digest algorithm: %s", algo)
	}
	if !hash.Available() {
		return 0, fmt.Errorf("Hash not avaialbe: %s", algo)
	}

	return hash, nil
}

// Equal checks if this MessageImprint is identical to another MessageImprint.
func (mi MessageImprint) Equal(other MessageImprint) bool {
	if !mi.HashAlgorithm.Algorithm.Equal(other.HashAlgorithm.Algorithm) {
		return false
	}
	if len(mi.HashAlgorithm.Parameters.Bytes) > 0 || len(other.HashAlgorithm.Parameters.Bytes) > 0 {
		if !bytes.Equal(mi.HashAlgorithm.Parameters.FullBytes, other.HashAlgorithm.Parameters.FullBytes) {
			return false
		}
	}
	if !bytes.Equal(mi.HashedMessage, other.HashedMessage) {
		return false
	}
	return true
}

// Accuracy ::= SEQUENCE {
//   seconds        INTEGER              OPTIONAL,
//   millis     [0] INTEGER  (1..999)    OPTIONAL,
//   micros     [1] INTEGER  (1..999)    OPTIONAL  }
type Accuracy struct {
	Seconds int `asn1:"optional"`
	Millis  int `asn1:"tag:0,optional"`
	Micros  int `asn1:"tag:1,optional"`
}

// Duration returns this Accuracy as a time.Duration.
func (a Accuracy) Duration() time.Duration {
	return 0 +
		time.Duration(a.Seconds)*time.Second +
		time.Duration(a.Millis)*time.Millisecond +
		time.Duration(a.Micros)*time.Microsecond
}