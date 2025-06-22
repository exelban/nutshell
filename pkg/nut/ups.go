package nut

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type UPS struct {
	Client       *Client
	Server       string
	PoolInterval time.Duration

	ID           string
	Name         string
	Description  string
	Manufacturer string
	Model        string
	VendorID     string
	ProductID    string

	Clients   []string
	Variables []Variable
	Commands  []Command
}

// https://networkupstools.org/docs/developer-guide.chunked/_variables.html
type Variable struct {
	Name          string
	Value         any
	Type          string
	Description   string
	Writeable     bool
	MaximumLength int
	OriginalType  string
}

type Command struct {
	Name        string
	Description string
}

var NUTStatusHumanReadable = map[string]string{
	"OL":      "Online",
	"OB":      "On Battery",
	"LB":      "Low Battery",
	"RB":      "Replace Battery",
	"CHRG":    "Charging",
	"DISCHRG": "Discharging",
	"BYPASS":  "Bypass Active",
	"CAL":     "Calibrating",
	"OFF":     "Offline",
	"OVER":    "Overload",
	"TRIM":    "SmartTrim",
	"BOOST":   "SmartBoost",
	"FSD":     "Forced Shutdown",
	"ALARM":   "Alarm",
	"TEST":    "Self Test",
	"COMM":    "Communication Lost",
}

func NewUPS(ctx context.Context, client *Client, server, name string, poolInterval time.Duration) (*UPS, error) {
	u := &UPS{
		Client:       client,
		Server:       server,
		PoolInterval: poolInterval,
		Name:         name,
	}

	if _, err := u.GetDescription(); err != nil {
		return nil, fmt.Errorf("failed to get UPS description: %w", err)
	}
	if _, err := u.GetClients(); err != nil {
		return nil, fmt.Errorf("failed to get UPS clients: %w", err)
	}
	if _, err := u.GetCommands(); err != nil {
		return nil, fmt.Errorf("failed to get UPS commands: %w", err)
	}
	if _, err := u.GetVariables(); err != nil {
		return nil, fmt.Errorf("failed to get UPS variables: %w", err)
	}

	for _, variable := range u.Variables {
		if variable.Name == "ups.mfr" {
			u.Manufacturer = variable.Value.(string)
		}
		if variable.Name == "ups.model" {
			u.Model = variable.Value.(string)
		}
		if variable.Name == "ups.vendorid" {
			if val, ok := variable.Value.(string); ok {
				u.VendorID = val
			} else if val, ok := variable.Value.(int64); ok {
				u.VendorID = strconv.FormatInt(val, 10)
			}
		}
		if variable.Name == "ups.productid" {
			if val, ok := variable.Value.(string); ok {
				u.ProductID = val
			} else if val, ok := variable.Value.(int64); ok {
				u.ProductID = strconv.FormatInt(val, 10)
			}
		}
		log.Printf("[DEBUG] %s: %s = %v", u.Name, variable.Name, variable.Value)
	}

	u.ID = u.GenerateID()

	tk := time.NewTicker(u.PoolInterval)
	go func() {
		for {
			select {
			case <-tk.C:
				if _, err := u.GetVariables(); err != nil {
					log.Printf("[ERROR] failed to poll %s variables: %v", u.Name, err)
					if err := u.Client.Reconnect(); err == nil {
						if _, err := u.GetVariables(); err != nil {
							log.Printf("[ERROR] retry after reconnect failed: %v", err)
						}
					} else {
						log.Printf("[ERROR] reconnect failed: %v", err)
					}
				}
			case <-ctx.Done():
				tk.Stop()
				return
			}
		}
	}()

	return u, nil
}

func (u *UPS) GenerateID() string {
	hasher := md5.New()
	input := []byte(u.Server)
	if u.Name != "" {
		input = append(input, []byte(u.Name)...)
	}
	if u.ProductID != "" {
		input = append(input, []byte(u.ProductID)...)
	}
	if u.VendorID != "" {
		input = append(input, []byte(u.VendorID)...)
	}
	hasher.Write(input)
	hash := hasher.Sum(nil)
	return base64.URLEncoding.EncodeToString(hash)[:6]
}

func (u *UPS) GetStatus() (string, string, error) {
	var statusCode string

	if value, ok := u.getVariable("ups.status").(string); ok {
		statusCode = value
	}

	var descriptions []string
	for _, code := range strings.Fields(statusCode) {
		if desc, ok := NUTStatusHumanReadable[code]; ok {
			if len(descriptions) > 0 {
				desc = strings.ToLower(desc)
			}
			descriptions = append(descriptions, desc)
		} else {
			descriptions = append(descriptions, "Unknown")
		}
	}

	return strings.Join(descriptions, ", "), statusCode, nil
}
func (u *UPS) GetBattery() (int64, int64, float64, error) {
	var charge int64 = 0
	var low int64 = 0
	var voltage = 0.0

	if value, ok := u.getVariable("battery.charge").(int64); ok {
		charge = value
	}
	if value, ok := u.getVariable("battery.charge.low").(int64); ok {
		low = value
	}
	if value, ok := u.getVariable("battery.voltage").(float64); ok {
		voltage = value
	}

	return charge, low, voltage, nil
}
func (u *UPS) GetLoad() (int64, int64, error) {
	var load int64 = 0
	if value, ok := u.getVariable("ups.load").(int64); ok {
		load = value
	}

	var power int64 = 0
	if value, ok := u.getVariable("ups.realpower").(int64); ok {
		power = value
	} else {
		if value, ok := u.getVariable("ups.realpower.nominal").(int64); ok {
			power = value
		} else if value, ok := u.getVariable("ups.power.nominal").(int64); ok {
			power = value
		}
		power = load * power / 100
	}

	return load, power, nil
}
func (u *UPS) GetRuntime() (int64, error) {
	if value, ok := u.getVariable("battery.runtime").(int64); ok {
		return value, nil
	}
	return 0, fmt.Errorf("battery.runtime variable not found")
}

func (u *UPS) GetDescription() (string, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("GET UPSDESC %s", u.Name))
	if err != nil {
		return "", fmt.Errorf("failed to get UPS description: %w", err)
	}
	description := strings.TrimPrefix(strings.Replace(resp[0], `"`, "", -1), fmt.Sprintf(`UPSDESC %s `, u.Name))
	u.Description = description
	return description, nil
}
func (u *UPS) GetClients() ([]string, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("LIST CLIENT %s", u.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to list clients: %w", err)
	}

	linePrefix := fmt.Sprintf("CLIENT %s ", u.Name)
	clientsList := []string{}
	for _, line := range resp[1 : len(resp)-1] {
		clientsList = append(clientsList, strings.TrimPrefix(line, linePrefix))
	}
	u.Clients = clientsList

	return clientsList, nil
}
func (u *UPS) GetCommands() ([]Command, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("LIST CMD %s", u.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to list commands: %w", err)
	}

	commandsList := []Command{}
	linePrefix := fmt.Sprintf("CMD %s ", u.Name)
	for _, line := range resp[1 : len(resp)-1] {
		cmdName := strings.TrimPrefix(line, linePrefix)
		cmd := Command{
			Name: cmdName,
		}
		description, err := u.GetCommandDescription(cmdName)
		if err != nil {
			return nil, fmt.Errorf("failed to get command description for %s: %w", cmdName, err)
		}
		cmd.Description = description
		commandsList = append(commandsList, cmd)
	}
	u.Commands = commandsList

	return commandsList, nil
}
func (u *UPS) GetVariables() ([]Variable, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("LIST VAR %s", u.Name))
	if err != nil {
		return nil, fmt.Errorf("failed to list variables: %w", err)
	}

	var vars []Variable
	offset := fmt.Sprintf("VAR %s ", u.Name)
	for _, line := range resp[1 : len(resp)-1] {
		cleanedLine := strings.TrimPrefix(line, offset)
		splitLine := strings.SplitN(cleanedLine, `"`, 3)
		if len(splitLine) < 2 {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(splitLine[0], " "))
		valueStr := strings.TrimSpace(splitLine[1])

		description, err := u.GetVariableDescription(name)
		if err != nil {
			return nil, err
		}
		varType, writeable, maximumLength, err := u.GetVariableType(name)
		if err != nil {
			return nil, err
		}

		newVar := Variable{
			Name:          name,
			Description:   description,
			Type:          varType,
			Writeable:     writeable,
			MaximumLength: maximumLength,
			Value:         valueStr,
			OriginalType:  varType,
		}

		switch valueStr {
		case "enabled":
			newVar.Value = true
			newVar.Type = "BOOLEAN"
		case "disabled":
			newVar.Value = false
			newVar.Type = "BOOLEAN"
		default:
			if matched, _ := regexp.MatchString(`^-?\d+(\.\d+)?$`, valueStr); matched {
				if strings.Contains(valueStr, ".") {
					if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
						newVar.Value = f
						newVar.Type = "FLOAT_64"
					}
				} else {
					if i, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
						newVar.Value = i
						newVar.Type = "INTEGER"
					}
				}
			} else {
				newVar.Type = "STRING"
			}
		}

		vars = append(vars, newVar)
	}
	u.Variables = vars

	return vars, nil
}

func (u *UPS) GetCommandDescription(commandName string) (string, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("GET CMDDESC %s %s", u.Name, commandName))
	if err != nil {
		return "", fmt.Errorf("failed to get command description: %w", err)
	}

	trimmedLine := strings.TrimPrefix(resp[0], fmt.Sprintf("CMDDESC %s %s ", u.Name, commandName))
	description := strings.Replace(trimmedLine, `"`, "", -1)

	return description, nil
}
func (u *UPS) GetVariableDescription(variableName string) (string, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("GET DESC %s %s", u.Name, variableName))
	if err != nil {
		return "", fmt.Errorf("failed to get variable description: %w", err)
	}

	trimmedLine := strings.TrimPrefix(resp[0], fmt.Sprintf("DESC %s %s ", u.Name, variableName))
	description := strings.Replace(trimmedLine, `"`, "", -1)

	return description, nil
}
func (u *UPS) GetVariableType(variableName string) (string, bool, int, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("GET TYPE %s %s", u.Name, variableName))
	if err != nil {
		return "UNKNOWN", false, -1, fmt.Errorf("failed to get type of variable %s: %w", variableName, err)
	}

	trimmedLine := strings.TrimPrefix(resp[0], fmt.Sprintf("TYPE %s %s ", u.Name, variableName))
	splitLine := strings.Split(trimmedLine, " ")
	writeable := splitLine[0] == "RW"
	varType := "UNKNOWN"
	maximumLength := 0
	if writeable {
		varType = splitLine[1]
		if strings.HasPrefix(varType, "STRING:") {
			splitType := strings.Split(varType, ":")
			varType = splitType[0]
			maximumLength, err = strconv.Atoi(splitType[1])
			if err != nil {
				return varType, writeable, -1, err
			}
		}
	} else {
		varType = splitLine[0]
	}

	return varType, writeable, maximumLength, nil
}

func (u *UPS) ForceShutdown() (bool, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("FSD %s", u.Name))
	if err != nil {
		return false, fmt.Errorf("failed to send force shutdown command: %w", err)
	}
	if len(resp) == 0 || resp[0] != "OK FSD-SET" {
		return false, fmt.Errorf("force shutdown command failed: %s", resp)
	}
	return true, nil
}

func (u *UPS) SetVariable(variableName, value string) (bool, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf(`SET VAR %s %s "%s"`, u.Name, variableName, value))
	if err != nil {
		return false, err
	}
	if len(resp) == 0 || resp[0] != "OK" {
		return false, fmt.Errorf("failed to set variable %s to %s: %s", variableName, value, resp)
	}
	return true, nil
}

func (u *UPS) SendCommand(commandName string) (bool, error) {
	resp, err := u.Client.sendCommand(fmt.Sprintf("INSTCMD %s %s", u.Name, commandName))
	if err != nil {
		return false, err
	}
	if len(resp) == 0 || resp[0] != "OK" {
		return false, fmt.Errorf("failed to send command %s: %s", commandName, resp)
	}
	return true, nil
}

func (u *UPS) getVariable(name string) any {
	for _, variable := range u.Variables {
		if variable.Name == name {
			return variable.Value
		}
	}
	return nil
}
