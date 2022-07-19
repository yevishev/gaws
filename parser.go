package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	pathPrefix       = "@openapi "
	paramPrefix      = "@openapiParam "
	tagsPrefix       = "@openapiTags "
	summaryPrefix    = "@openapiSummary "
	descPrefix       = "@openapiDesc "
	deprecatedPrefix = "@openapiDeprecated"
	requestPrefix    = "@openapiRequest "
	responsePrefix   = "@openapiResponse "
)

var (
	typesMap = map[string]string{
		"int":     "integer",
		"int8":    "integer",
		"int16":   "integer",
		"int32":   "integer",
		"int64":   "integer",
		"uint":    "integer",
		"uint8":   "integer",
		"uint16":  "integer",
		"uint32":  "integer",
		"uint64":  "integer",
		"float":   "number",
		"float32": "number",
		"float64": "number",
		"bool":    "boolean",
		"string":  "string",
		"[]byte":  "string",
	}

	formatsMap = map[string]string{
		"float":   "float",
		"float32": "float",
		"float64": "double",
		"[]byte":  "binary",
	}
)

type Parser struct {
	structs []Struct
	doc     *Doc
}

func NewParser(doc *Doc, structs []Struct) *Parser {
	return &Parser{
		structs: structs,
		doc:     doc,
	}
}

func (p *Parser) parseComment(comment string) (err error) {
	splits := strings.Split(comment, "\n")
	paths := map[string][]string{}
	endpoint := Endpoint{
		Responses: map[string]Response{},
	}

	for _, l := range splits {
		if strings.HasPrefix(l, pathPrefix) {
			method, path, err := p.parsePath(l)
			if err != nil {
				return wrapError(err, l)
			}
			paths[path] = append(paths[path], method)
		}
		if strings.HasPrefix(l, tagsPrefix) {
			endpoint.Tags = parseTags(l)
		}

		if strings.HasPrefix(l, summaryPrefix) {
			endpoint.Summary = parseSummary(l)
		}

		if strings.HasPrefix(l, descPrefix) {
			endpoint.Description = parseDesc(l)
		}

		if strings.HasPrefix(l, deprecatedPrefix) {
			endpoint.Deprecated = true
		}

		if strings.HasPrefix(l, paramPrefix) {
			param, err := p.parseParam(l)
			if err != nil {
				return wrapError(err, l)
			}
			endpoint.Parameters = append(endpoint.Parameters, param)
		}

		if strings.HasPrefix(l, requestPrefix) {
			request, err := p.parseRequest(l)
			if err != nil {
				return wrapError(err, l)
			}
			endpoint.RequestBody = request
		}

		if strings.HasPrefix(l, responsePrefix) {
			status, contentType, content, err := p.parseResponse(l)
			if err != nil {
				return wrapError(err, l)
			}

			endpoint.Responses[status] = Response{
				Content: map[string]Content{
					contentType: content,
				},
			}
		}
	}

	if len(paths) == 0 {
		return nil
	}

	if len(endpoint.Responses) == 0 {
		for path := range paths {
			for _, method := range paths[path] {
				return fmt.Errorf("No %s for: %s %s", trim(responsePrefix), upper(method), path)
			}
		}
	}

	for path := range paths {
		for _, method := range paths[path] {

			if _, ex := p.doc.Paths[path]; !ex {
				p.doc.Paths[path] = Path{
					method: endpoint,
				}
			} else {
				p.doc.Paths[path][method] = endpoint
			}
		}
	}

	return nil
}

// parsePath @openapi GET /foo/bar
func (p *Parser) parsePath(s string) (method, path string, err error) {
	s = strings.TrimPrefix(s, pathPrefix)
	splits := strings.Split(s, " ")

	method = strings.ToLower(trim(splits[0]))
	path = trim(getStr(splits, 1))
	err = validatePath(method, path)

	return
}

// parseRequest @openapiParam foo in=path, type=int, default=1, required=true
func (p *Parser) parseParam(s string) (Parameter, error) {
	s = strings.TrimPrefix(s, paramPrefix)
	splits := strings.SplitN(s, " ", 2)

	params, err := parseParams(getStr(splits, 1))
	if err != nil {
		return Parameter{}, err
	}

	format := params["format"]
	if format == "" {
		format = formatsMap[params["type"]]
	}

	if !strIn(params["type"], paramTypes) {
		params["type"] = typesMap[params["type"]]
	}

	required := params["required"] == "true"
	if params["required"] == "" && params["in"] == "path" {
		required = true
	}

	param := Parameter{
		Name:     trim(splits[0]),
		In:       params["in"],
		Required: required,
		Schema: &Property{
			Type:    params["type"],
			Format:  format,
			Example: params["example"],
			Default: params["default"],
			Description: params["description"],
		},
	}

	return param, validateParam(param)
}

// parseRequest @openapiRequest application/json {"foo": "bar"}
func (p *Parser) parseRequest(s string) (body RequestBody, err error) {
	s = strings.TrimPrefix(s, requestPrefix)
	splits := strings.SplitN(s, " ", 2)

	contentType := trim(splits[0])
	request := trim(getStr(splits, 1))

	content, err := p.parseSchema(request)
	if err != nil {
		return body, err
	}

	body.Content = map[string]Content{
		contentType: content,
	}

	return body, validateRequest(body)
}

// parseResponse @openapiResponse 200 application/json {"foo": "bar"}
func (p *Parser) parseResponse(s string) (status string, contentType string, content Content, err error) {
	s = strings.TrimPrefix(s, responsePrefix)
	splits := strings.SplitN(s, " ", 3)

	status = trim(splits[0])
	contentType = trim(getStr(splits, 1))
	response := trim(getStr(splits, 2))

	if contentType == "application/octet-stream" {
		content.Schema = Schema{
			Type:   "string",
			Format: "binary",
		}
	} else {
		content, err = p.parseSchema(response)
		if err != nil {
			return status, contentType, content, err
		}
	}

	err = validateResponse(status, contentType, content)

	return status, contentType, content, err
}

// parseSchema {"foo": "bar"}
func (p *Parser) parseSchema(s string) (Content, error) {
	content := Content{}
	if strings.HasPrefix(s, "{") {
		if json.Valid([]byte(s)) {
			content.Example = s
			return content, nil
		}

		fields, err := parseJSONSchema(s)
		if err != nil {
			return content, err
		}

		content.Schema.Type = "object"
		content.Schema.Properties = map[string]Property{}
		for n, t := range fields {
			property, err := p.typeToProperty("", t)
			if err != nil {
				return content, err
			}
			content.Schema.Properties[n] = property
		}

		return content, nil
	}

	schema, err := p.parseStruct(s)
	if err != nil {
		return content, err
	}
	content.Schema = schema

	return content, nil
}

// parseStruct User
func (p *Parser) parseStruct(s string) (Schema, error) {
	schema := Schema{
		Properties: map[string]Property{},
	}

	if strings.HasPrefix(s, "[]") {
		arraySchema, err := p.parseStruct(strings.TrimPrefix(s, "[]"))
		if err != nil {
			return schema, err
		}
		schema.Type = "array"
		schema.Properties = nil
		schema.Items = &arraySchema
		return schema, nil
	}

	st := p.structByName(s)
	if st == nil {
		return schema, fmt.Errorf("Unknown type: %s", s)
	}

	schema.Type = "object"
	for i := range st.Fields {
		name := st.Fields[i].Name
		tag := getTag(st.Fields[i].Tag, "json")
		if tag == "-" {
			continue
		}
		if tag != "" {
			name = tag
		}

		property, err := p.typeToProperty(getPkg(s), st.Fields[i].Type)
		if err != nil {
			return schema, err
		}

		formats, err := getFormatFromTag(st.Fields[i].Tag)
		if err != nil {
			return schema, err
		}
		if formats["type"] != "" {
			property.Type = formats["type"]
		}
		if formats["format"] != "" {
			property.Format = formats["format"]
		}
		if formats["example"] != "" {
			property.Example = formats["example"]
		}

		schema.Properties[name] = property
	}

	return schema, nil
}

func (p *Parser) structByName(name string) *Struct {
	for i := range p.structs {
		if p.structs[i].Name == name {
			return &p.structs[i]
		}
	}

	return nil
}

func (p *Parser) typeToProperty(pkg, t string) (property Property, err error) {
	t = strings.TrimPrefix(t, "*")
	if isBaseType(t) {
		property.Type = typesMap[t]
		property.Format = formatsMap[t]
		return
	}
	if isTime(t) {
		property.Type = "string"
		property.Format = "date-time"
		return
	}

	if strings.HasPrefix(t, "[]") {
		property.Type = "array"
		prop, err := p.typeToProperty(pkg, strings.TrimPrefix(t, "[]"))
		if err != nil {
			return property, err
		}

		property.Items = &Schema{
			Type: prop.Type,
		}

		if prop.Type == "object" {
			property.Items.Properties = prop.Properties
		}
		if prop.Type == "array" {
			property.Items.Items = prop.Items
		}

		return property, nil
	}

	// Is alias
	st := p.structByName(t)
	if st != nil && st.Origin != "" {
		return p.typeToProperty(pkg, st.Origin)
	}

	// Is struct
	schema, err := p.parseStruct(addPkg(pkg, t))
	if err != nil {
		return property, err
	}
	property.Type = "object"
	property.Properties = schema.Properties

	return property, nil
}

// parseTags @openapiTags foo, bar
func parseTags(s string) []string {
	s = strings.TrimPrefix(s, tagsPrefix)
	tags := []string{}
	for _, t := range strings.Split(s, ",") {
		t = trim(t)
		if t != "" {
			tags = append(tags, t)
		}
	}

	return tags
}

//parseSummary @openapiSummary Some text
func parseSummary(s string) string {
	return strings.TrimPrefix(s, summaryPrefix)
}

//parseDesc @openapiDesc Some text
func parseDesc(s string) string {
	return strings.TrimPrefix(s, descPrefix)
}

func isBaseType(t string) bool {
	return typesMap[t] != ""
}

func isTime(t string) bool {
	return t == "time.Time"
}

func wrapError(err error, comment string) error {
	return fmt.Errorf("%s (%s)", err.Error(), comment)
}
