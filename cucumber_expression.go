package cucumberexpressions

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

const alternativesMayNotBeEmpty = "Alternative may not be empty: %s"
const parameterTypesCanNotBeAlternative = "Parameter types cannot be alternative: %s"
const parameterTypesCanNotBeOptional = "Parameter types cannot be optional: %s"
const alternativeMayNotExclusivelyContainOptionals = "Alternative may not exclusively contain optionals: %s"
const couldNotRewrite = "Could not rewrite %s"
const optionalMayNotBeEmpty = "Optional may not be empty: %s"

var escapeRegexp = regexp.MustCompile(`([\\^\[({$.|?*+})\]])`)

type CucumberExpression struct {
	source                string
	parameterTypes        []*ParameterType
	treeRegexp            *TreeRegexp
	parameterTypeRegistry *ParameterTypeRegistry
}

func NewCucumberExpression(expression string, parameterTypeRegistry *ParameterTypeRegistry) (Expression, error) {
	result := &CucumberExpression{source: expression, parameterTypeRegistry: parameterTypeRegistry}

	ast, err := parse(expression)
	if err != nil {
		return nil, err
	}

	pattern, err := result.rewriteNodeToRegex(ast)
	if err != nil {
		return nil, err
	}
	result.treeRegexp = NewTreeRegexp(regexp.MustCompile(pattern))
	return result, nil
}

func (c *CucumberExpression) rewriteNodeToRegex(node astNode) (string, error) {
	switch node.nodeType {
	case textNode:
		return c.processEscapes(node.token.text), nil
	case optionalNode:
		return c.rewriteOptional(node)
	case alternationNode:
		return c.rewriteAlternation(node)
	case alternativeNode:
		return c.rewriteAlternative(node)
	case parameterNode:
		return c.rewriteParameter(node)
	case expressionNode:
		return c.rewriteExpression(node)
	default:
		return "", NewCucumberExpressionError(fmt.Sprintf(couldNotRewrite, c.source))
	}
}

func (c *CucumberExpression) processEscapes(expression string) string {
	return escapeRegexp.ReplaceAllString(expression, `\$1`)
}

func (c *CucumberExpression) rewriteOptional(node astNode) (string, error) {
	err := c.assertNoParameters(node, parameterTypesCanNotBeOptional)
	if err != nil {
		return "", err
	}
	err = c.assertNotEmpty(node, optionalMayNotBeEmpty)
	if err != nil {
		return "", err
	}
	return c.rewriteNodesToRegex(node.nodes, "", "(?:", ")?")
}

func (c *CucumberExpression) rewriteAlternation(node astNode) (string, error) {
	// Make sure the alternative parts aren't empty and don't contain parameter types
	for _, alternative := range node.nodes {
		if len(alternative.nodes) == 0 {
			return "", NewCucumberExpressionError(fmt.Sprintf(alternativesMayNotBeEmpty, c.source))
		}
		err := c.assertNoParameters(alternative, parameterTypesCanNotBeAlternative)
		if err != nil {
			return "", err
		}
		err = c.assertNotEmpty(alternative, alternativeMayNotExclusivelyContainOptionals)
		if err != nil {
			return "", err
		}
	}
	return c.rewriteNodesToRegex(node.nodes, "|", "(?:", ")")
}

func (c *CucumberExpression) rewriteAlternative(node astNode) (string, error) {
	return c.rewriteNodesToRegex(node.nodes, "", "", "")
}

func (c *CucumberExpression) rewriteParameter(node astNode) (string, error) {
	buildCaptureRegexp := func(regexps []*regexp.Regexp) string {
		if len(regexps) == 1 {
			return fmt.Sprintf("(%s)", regexps[0].String())
		}

		captureGroups := make([]string, len(regexps))
		for i, r := range regexps {
			captureGroups[i] = fmt.Sprintf("(?:%s)", r.String())
		}

		return fmt.Sprintf("(%s)", strings.Join(captureGroups, "|"))
	}

	typeName := node.text()
	err := CheckParameterTypeName(typeName)
	if err != nil {
		return "", err
	}
	parameterType := c.parameterTypeRegistry.LookupByTypeName(typeName)
	if parameterType == nil {
		err = NewUndefinedParameterTypeError(typeName)
		return "", err
	}
	c.parameterTypes = append(c.parameterTypes, parameterType)
	return buildCaptureRegexp(parameterType.regexps), nil
}

func (c *CucumberExpression) rewriteExpression(node astNode) (string, error) {
	return c.rewriteNodesToRegex(node.nodes, "", "^", "$")
}

func (c *CucumberExpression) rewriteNodesToRegex(nodes []astNode, delimiter string, prefix string, suffix string) (string, error) {
	builder := strings.Builder{}
	builder.WriteString(prefix)
	for i, node := range nodes {
		if i > 0 {
			builder.WriteString(delimiter)
		}
		s, err := c.rewriteNodeToRegex(node)
		if err != nil {
			return s, err
		}
		builder.WriteString(s)
	}
	builder.WriteString(suffix)
	return builder.String(), nil
}

func (c *CucumberExpression) assertNotEmpty(node astNode, message string) error {
	for _, node := range node.nodes {
		if node.nodeType == textNode {
			return nil
		}
	}
	return NewCucumberExpressionError(fmt.Sprintf(message, c.source))
}

func (c *CucumberExpression) assertNoParameters(node astNode, message string) error {
	for _, node := range node.nodes {
		if node.nodeType == parameterNode {
			return NewCucumberExpressionError(fmt.Sprintf(message, c.source))
		}
	}
	return nil
}

func (c *CucumberExpression) Match(text string, typeHints ...reflect.Type) ([]*Argument, error) {
	hintOrDefault := func(i int, typeHints ...reflect.Type) reflect.Type {
		typeHint := reflect.TypeOf("")
		if i < len(typeHints) {
			typeHint = typeHints[i]
		}
		return typeHint
	}

	parameterTypes := make([]*ParameterType, len(c.parameterTypes))
	copy(parameterTypes, c.parameterTypes)
	for i := 0; i < len(parameterTypes); i++ {
		if parameterTypes[i].isAnonymous() {
			typeHint := hintOrDefault(i, typeHints...)
			parameterType, err := parameterTypes[i].deAnonymize(typeHint, c.objectMapperTransformer(typeHint))
			if err != nil {
				return nil, err
			}
			parameterTypes[i] = parameterType
		}
	}
	return BuildArguments(c.treeRegexp, text, parameterTypes), nil
}

func (c *CucumberExpression) Regexp() *regexp.Regexp {
	return c.treeRegexp.Regexp()
}

func (c *CucumberExpression) Source() string {
	return c.source
}

func (c *CucumberExpression) objectMapperTransformer(typeHint reflect.Type) func(args ...*string) interface{} {
	return func(args ...*string) interface{} {
		i, err := c.parameterTypeRegistry.defaultTransformer.Transform(*args[0], typeHint)
		if err != nil {
			panic(err)
		}
		return i
	}
}
