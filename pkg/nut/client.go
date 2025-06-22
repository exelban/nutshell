package nut

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

type Client struct {
	Version         string
	ProtocolVersion string
	Hostname        net.Addr
	conn            *net.TCPConn

	list map[string]*UPS

	hostname string
	port     string
	username string
	password string

	poolInterval time.Duration
}

func New(ctx context.Context, hostname, port, username, password string, poolInterval time.Duration) (*Client, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%s", hostname, port))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve TCP address: %s", err)
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %s", err)
	}

	client := &Client{
		Hostname: conn.RemoteAddr(),
		conn:     conn,

		list: make(map[string]*UPS),

		hostname: hostname,
		port:     port,
		username: username,
		password: password,

		poolInterval: poolInterval,
	}

	status, err := client.authenticate(username, password)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %s", err)
	}
	if !status {
		return nil, fmt.Errorf("authentication failed, check username and password")
	}

	if _, err := client.getVersion(); err != nil {
		return nil, fmt.Errorf("failed to get version: %s", err)
	}
	if _, err := client.getNetworkProtocolVersion(); err != nil {
		return nil, fmt.Errorf("failed to get network protocol version: %s", err)
	}
	if err := client.getListOfUPS(ctx); err != nil {
		return nil, fmt.Errorf("failed to get list of UPS: %s", err)
	}

	return client, nil
}

func (c *Client) Reconnect() error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%s", c.hostname, c.port))
	if err != nil {
		return fmt.Errorf("failed to resolve TCP address: %s", err)
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return fmt.Errorf("failed to reconnect to server: %s", err)
	}
	c.conn = conn
	c.Hostname = conn.RemoteAddr()

	status, err := c.authenticate(c.username, c.password)
	if err != nil {
		return fmt.Errorf("failed to authenticate after reconnect: %s", err)
	}
	if !status {
		return fmt.Errorf("authentication failed after reconnect")
	}
	if _, err := c.getVersion(); err != nil {
		return fmt.Errorf("failed to get version after reconnect: %s", err)
	}
	if _, err := c.getNetworkProtocolVersion(); err != nil {
		return fmt.Errorf("failed to get network protocol version after reconnect: %s", err)
	}
	return nil
}
func (c *Client) Disconnect() error {
	resp, err := c.sendCommand("LOGOUT")
	if err != nil {
		return fmt.Errorf("failed to send logout: %s", err)
	}
	if len(resp) <= 0 || (resp[0] != "OK Goodbye" && resp[0] != "Goodbye...") {
		return fmt.Errorf("logout did not succeed")
	}
	return nil
}

func (c *Client) UPSs() ([]*UPS, error) {
	if len(c.list) == 0 {
		return nil, fmt.Errorf("no UPSs found")
	}

	upsList := make([]*UPS, 0, len(c.list))
	for _, ups := range c.list {
		upsList = append(upsList, ups)
	}

	return upsList, nil
}
func (c *Client) UPS(name string) (*UPS, error) {
	if ups, ok := c.list[name]; ok {
		return ups, nil
	}
	return nil, fmt.Errorf("UPS %s not found", name)
}

// sendCommand sends a command to the NUT server
// readResponse parses the response from the NUT server
func (c *Client) sendCommand(cmd string) ([]string, error) {
	cmd = fmt.Sprintf("%v\n", cmd)
	endLine := fmt.Sprintf("END %s", cmd)
	if strings.HasPrefix(cmd, "USERNAME ") || strings.HasPrefix(cmd, "PASSWORD ") || strings.HasPrefix(cmd, "SET ") || strings.HasPrefix(cmd, "HELP ") || strings.HasPrefix(cmd, "VER ") || strings.HasPrefix(cmd, "NETVER ") {
		endLine = "OK\n"
	}
	if _, err := fmt.Fprint(c.conn, cmd); err != nil {
		return nil, fmt.Errorf("failed to send command: %s", err)
	}

	resp, err := c.readResponse(endLine, strings.HasPrefix(cmd, "LIST "))
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(resp[0], "ERR ") {
		return nil, fmt.Errorf(strings.Split(resp[0], " ")[1])
	}

	return resp, nil
}
func (c *Client) readResponse(endLine string, multiLineResponse bool) ([]string, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	buff := bufio.NewReader(c.conn)
	response := []string{}

	for {
		line, err := buff.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("error reading response: %v", err)
		}
		if len(line) > 0 {
			cleanLine := strings.TrimSuffix(line, "\n")
			lines := strings.Split(cleanLine, "\n")
			response = append(response, lines...)
			if line == endLine || multiLineResponse == false {
				break
			}
		}
	}

	return response, nil
}

// authenticate the existing NUT session with provided username and password.
func (c *Client) authenticate(username, password string) (bool, error) {
	resp, err := c.sendCommand(fmt.Sprintf("USERNAME %s", username))
	if err != nil {
		return false, fmt.Errorf("failed to send USERNAME command: %s", err)
	}
	if resp[0] != "OK" {
		return false, fmt.Errorf("invalid response to USERNAME: %v", err)
	}

	resp, err = c.sendCommand(fmt.Sprintf("PASSWORD %s", password))
	if err != nil {
		return false, fmt.Errorf("failed to send PASSWORD command: %s", err)
	}
	if resp[0] != "OK" {
		return false, fmt.Errorf("invalid response to PASSWORD: %v", err)
	}

	return true, nil
}

// getListOfUPS retrieves the list of UPS devices from the server.
// getVersion returns the version of the server currently in use.
// getNetworkProtocolVersion returns the version of the network protocol currently in use.
func (c *Client) getListOfUPS(ctx context.Context) error {
	resp, err := c.sendCommand("LIST UPS")
	if err != nil {
		return fmt.Errorf("failed to get UPS list: %s", err)
	}

	for _, line := range resp {
		if strings.HasPrefix(line, "UPS ") {
			splitLine := strings.Split(strings.TrimPrefix(line, "UPS "), `"`)
			name := strings.TrimSuffix(splitLine[0], " ")
			ups, err := NewUPS(ctx, c, fmt.Sprintf("%s:%s", c.hostname, c.port), name, c.poolInterval)
			if err != nil {
				log.Printf("[ERROR] failed to create UPS %s: %s", name, err)
				continue
			}
			if _, ok := c.list[ups.ID]; !ok {
				c.list[ups.ID] = ups
			}
		}
	}

	return nil
}
func (c *Client) getVersion() (string, error) {
	resp, err := c.sendCommand("VER")
	if err != nil || len(resp) < 1 {
		return "", fmt.Errorf("failed to get version: %s", err)
	}
	c.Version = resp[0]
	return resp[0], err
}
func (c *Client) getNetworkProtocolVersion() (string, error) {
	resp, err := c.sendCommand("NETVER")
	if err != nil || len(resp) < 1 {
		return "", fmt.Errorf("failed to get network protocol version: %s", err)
	}
	c.ProtocolVersion = resp[0]
	return resp[0], err
}
