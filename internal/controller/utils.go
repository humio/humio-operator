package controller

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"golang.org/x/exp/constraints"
)

// GetKeyWithHighestValue returns the key corresponding to the highest value in a map. In case multiple keys have the same value, the first key is returned.
//
// An error is returned if the passed map is empty.
func GetKeyWithHighestValue[K comparable, V constraints.Ordered](inputMap map[K]V) (K, error) {
	if len(inputMap) == 0 {
		var zeroKey K
		return zeroKey, errors.New("map is empty")
	}

	var maxKey K
	var maxValue V
	firstIteration := true

	for k, v := range inputMap {
		if firstIteration || v > maxValue {
			maxKey = k
			maxValue = v
			firstIteration = false
		}
	}
	return maxKey, nil
}

// GetPodNameFromNodeUri extracts and returns the pod name from a given URI string. This is done by extracting the
// hostname from the URI, splitting it against the "." string, and returning the first part.
//
// Examples:
//   - for https://cloud-test-core-xbattq.svc.namespace:8080, cloud-test-core-xbattq is returned
//   - for http://cloud-test-core-xbattq:8080, cloud-test-core-xbattq is returned
//
// An error is returned in case the URI cannot be parsed, or if the hostname string split has 0 parts
func GetPodNameFromNodeUri(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	parts := strings.Split(u.Hostname(), ".")
	if len(parts) == 0 {
		return "", errors.New("unable to determine pod name")
	}
	return parts[0], nil
}

func RemoveIntFromSlice(slice []int, value int) []int {
	var result []int
	for _, v := range slice {
		if v != value {
			result = append(result, v)
		}
	}
	return result
}

func EncryptSecret(plaintext, key string) (string, error) {
	hash := sha256.Sum256([]byte(key))
	derivedKey := hash[:]
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func DecryptSecret(ciphertext, key string) (string, error) {
	hash := sha256.Sum256([]byte(key))
	derivedKey := hash[:]
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce := data[:nonceSize]
	ciphertextBytes := data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
