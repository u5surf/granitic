package validate

import (
	"errors"
	"fmt"
	"github.com/graniticio/granitic/ioc"
	rt "github.com/graniticio/granitic/reflecttools"
	"github.com/graniticio/granitic/types"
	"reflect"
	"strings"
)

const SliceRuleCode = "SLICE"

const (
	sliceOpRequiredCode = commonOpRequired
	sliceOpStopAllCode  = commonOpStopAll
	sliceOpMexCode      = commonOpMex
)

type sliceValidationOperation uint

const (
	SliceOpUnsupported = iota
	SliceOpRequired
	SliceOpStopAll
	SliceOpMex
)

func NewSliceValidator(field, defaultErrorCode string) *sliceValidator {
	bv := new(sliceValidator)
	bv.defaultErrorCode = defaultErrorCode
	bv.field = field
	bv.codesInUse = types.NewOrderedStringSet([]string{})
	bv.dependsFields = determinePathFields(field)
	bv.operations = make([]*sliceOperation, 0)
	bv.codesInUse.Add(bv.defaultErrorCode)

	return bv
}

type sliceValidator struct {
	stopAll             bool
	codesInUse          types.StringSet
	dependsFields       types.StringSet
	defaultErrorCode    string
	field               string
	missingRequiredCode string
	required            bool
	operations          []*sliceOperation
}

type sliceOperation struct {
	OpType    sliceValidationOperation
	ErrCode   string
	MExFields types.StringSet
}

func (bv *sliceValidator) IsSet(field string, subject interface{}) (bool, error) {

	ps, err := bv.extractValue(field, subject)

	if err != nil {
		return false, err
	}

	if ps == nil {
		return false, nil
	}

	return true, nil
}

func (bv *sliceValidator) Validate(vc *validationContext) (result *ValidationResult, unexpected error) {

	f := bv.field

	if vc.OverrideField != "" {
		f = vc.OverrideField
	}

	sub := vc.Subject

	r := new(ValidationResult)
	set, err := bv.IsSet(f, sub)

	if err != nil {
		return nil, err

	} else if !set {
		r.Unset = true

		if bv.required {
			r.ErrorCodes = []string{bv.missingRequiredCode}
		} else {
			r.ErrorCodes = []string{}
		}

		return r, nil
	}

	//Ignoring error as called previously during IsSet
	value, _ := bv.extractValue(f, sub)

	return bv.runOperations(value, vc, r.ErrorCodes)
}

func (bv *sliceValidator) runOperations(i interface{}, vc *validationContext, errors []string) (*ValidationResult, error) {

	if errors == nil {
		errors = []string{}
	}

	ec := types.NewOrderedStringSet(errors)

	for _, op := range bv.operations {

		switch op.OpType {
		case SliceOpMex:
			checkMExFields(op.MExFields, vc, ec, op.ErrCode)
		}
	}

	r := new(ValidationResult)
	r.ErrorCodes = ec.Contents()

	return r, nil

}

func (bv *sliceValidator) extractValue(f string, s interface{}) (interface{}, error) {

	v, err := rt.FindNestedField(rt.ExtractDotPath(f), s)

	if err != nil {
		return nil, err
	}

	if rt.NilPointer(v) {
		return nil, nil
	}

	if v.IsValid() && v.Kind() == reflect.Slice {

		if v.IsNil() {
			return nil, nil
		}

		return v.Interface(), nil
	}

	m := fmt.Sprintf("%s is not a slice", f)

	return nil, errors.New(m)

}

func (bv *sliceValidator) StopAllOnFail() bool {
	return bv.stopAll
}

func (bv *sliceValidator) CodesInUse() types.StringSet {
	return bv.codesInUse
}

func (bv *sliceValidator) DependsOnFields() types.StringSet {

	return bv.dependsFields
}

func (bv *sliceValidator) StopAll() *sliceValidator {

	bv.stopAll = true

	return bv
}

func (bv *sliceValidator) Required(code ...string) *sliceValidator {

	bv.required = true
	bv.missingRequiredCode = bv.chooseErrorCode(code)

	return bv
}

func (bv *sliceValidator) MEx(fields types.StringSet, code ...string) *sliceValidator {
	op := new(sliceOperation)
	op.ErrCode = bv.chooseErrorCode(code)
	op.OpType = SliceOpMex
	op.MExFields = fields

	bv.addOperation(op)

	return bv
}

func (bv *sliceValidator) addOperation(o *sliceOperation) {
	bv.operations = append(bv.operations, o)
}

func (bv *sliceValidator) chooseErrorCode(v []string) string {

	if len(v) > 0 {
		bv.codesInUse.Add(v[0])
		return v[0]
	} else {
		return bv.defaultErrorCode
	}

}

func (bv *sliceValidator) Operation(c string) (sliceValidationOperation, error) {
	switch c {
	case sliceOpRequiredCode:
		return SliceOpRequired, nil
	case sliceOpStopAllCode:
		return SliceOpStopAll, nil
	case sliceOpMexCode:
		return SliceOpMex, nil
	}

	m := fmt.Sprintf("Unsupported bool validation operation %s", c)
	return SliceOpUnsupported, errors.New(m)

}

func NewSliceValidatorBuilder(ec string, cf ioc.ComponentByNameFinder) *sliceValidatorBuilder {
	bv := new(sliceValidatorBuilder)
	bv.componentFinder = cf
	bv.defaultErrorCode = ec

	return bv
}

type sliceValidatorBuilder struct {
	defaultErrorCode string
	componentFinder  ioc.ComponentByNameFinder
}

func (vb *sliceValidatorBuilder) parseRule(field string, rule []string) (Validator, error) {

	defaultErrorcode := DetermineDefaultErrorCode(SliceRuleCode, rule, vb.defaultErrorCode)
	bv := NewSliceValidator(field, defaultErrorcode)

	for _, v := range rule {

		ops := DecomposeOperation(v)
		opCode := ops[0]

		if IsTypeIndicator(SliceRuleCode, opCode) {
			continue
		}

		op, err := bv.Operation(opCode)

		if err != nil {
			return nil, err
		}

		switch op {
		case SliceOpRequired:
			err = vb.markRequired(field, ops, bv)
		case SliceOpStopAll:
			bv.StopAll()
		case SliceOpMex:
			err = vb.captureExclusiveFields(field, ops, bv)
		}

		if err != nil {

			return nil, err
		}

	}

	return bv, nil

}

func (vb *sliceValidatorBuilder) captureExclusiveFields(field string, ops []string, bv *sliceValidator) error {
	_, err := paramCount(ops, "MEX", field, 2, 3)

	if err != nil {
		return err
	}

	members := strings.SplitN(ops[1], setMemberSep, -1)
	fields := types.NewOrderedStringSet(members)

	bv.MEx(fields, extractVargs(ops, 3)...)

	return nil

}

func (vb *sliceValidatorBuilder) markRequired(field string, ops []string, bv *sliceValidator) error {

	_, err := paramCount(ops, "Required", field, 1, 2)

	if err != nil {
		return err
	}

	bv.Required(extractVargs(ops, 2)...)

	return nil
}
