package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type ClusterStatus struct {
	ClusterName string `json:"cluster_name"`
	Status      string `json:"status"`
	NodeCount   int    `json:"number_of_nodes"`
}

type ClusterLicense struct {
	Status     string    `json:"status"`
	UID        string    `json:"uid"`
	Type       string    `json:"type"`
	Expiry     int64     `json:"expiry_date_in_millis"`
	ExpiryTime time.Time `json:"-"`
	IssuedTo   string    `json:"issued_to"`
	MaxNodes   int       `json:"max_nodes"`
}

type Cluster struct {
	Hostname string         `json:"hostname"`
	UseSSL   bool           `json:"usessl"`
	Username string         `json:"username"`
	Password string         `json:"password"`
	Status   ClusterStatus  `json:"-"`
	License  ClusterLicense `json:"-"`
	stopchan chan struct{}
}

func (c *Cluster) ClusterRequest(path string, body io.Reader, s interface{}) error {
	log.Println("GET ", path)
	req, err := http.NewRequest("GET", path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)

	transport := http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := http.Client{
		Transport: &transport,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// b, _ := ioutil.ReadAll(resp.Body)
	// log.Println(string(b))
	err = json.NewDecoder(resp.Body).Decode(s)

	return err

}

func (c *Cluster) BuildPath(path string) string {
	if c.UseSSL {
		return fmt.Sprintf("https://%s:9200/%s", c.Hostname, path)
	}
	return fmt.Sprintf("http://%s:9200/%s", c.Hostname, path)
}

func (c *Cluster) GetClusterStatus() (ClusterStatus, error) {
	path := c.BuildPath("_cluster/health")
	var status ClusterStatus
	err := c.ClusterRequest(path, nil, &status)
	return status, err
}

func (c *Cluster) GetClusterLicense() (ClusterLicense, error) {
	path := c.BuildPath("_xpack/license")
	var license ClusterLicense
	err := c.ClusterRequest(path, nil, &struct {
		*ClusterLicense `json:"license"`
	}{
		&license,
	})

	license.ExpiryTime = time.Unix(0, license.Expiry*int64(time.Millisecond))

	return license, err
}

func (c *Cluster) Monitor(licensePath string) error {
	err := c.UpdateCluster()
	if err != nil {
		return err
	}

	if c.ShouldUpdateLicense() {
		err = c.UpdateLicense(licensePath)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Cluster) UpdateCluster() error {
	status, err := c.GetClusterStatus()
	if err != nil {
		return err
	}

	c.Status = status

	license, err := c.GetClusterLicense()
	if err != nil {
		return err
	}

	c.License = license
	return nil
}

func (c *Cluster) ShouldUpdateLicense() bool {
	license := c.License

	if license.Status != "active" {
		return true
	}

	epoch := time.Unix(license.Expiry, 0)
	if time.Until(epoch) <= 48*time.Hour {
		return true
	}

	return false
}

func ClusterJSONRequest(path string, body io.Reader, s interface{}) error {
	log.Println("POST ", path)
	req, err := http.NewRequest("POST", path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth("elastic", "changeme")
	req.Header.Set("Content-Type", "application/json")

	transport := http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := http.Client{
		Transport: &transport,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// b, _ := ioutil.ReadAll(resp.Body)
	// log.Println(string(b))
	err = json.NewDecoder(resp.Body).Decode(s)

	return err

}

func (c *Cluster) UpdateLicense(licensePath string) error {
	file, err := os.Open(licensePath)
	if err != nil {
		return err
	}

	acknowledged := false
	status := ""

	path := c.BuildPath("_xpack/license")
	err = ClusterJSONRequest(path, file, &struct {
		Acknowledged *bool   `json:"acknowledged"`
		Status       *string `json:"license_status"`
	}{
		Acknowledged: &acknowledged,
		Status:       &status,
	})

	if err != nil {
		return err
	}

	if acknowledged && status == "valid" {
		license, err := c.GetClusterLicense()
		if err != nil {
			return err
		}
		c.License = license
		return nil
	}

	return fmt.Errorf("not able to set license")
}
