package validate

import (
	"errors"
	"fmt"
	"github.com/graniticio/granitic/ioc"
	rt "github.com/graniticio/granitic/reflecttools"
	"github.com/graniticio/granitic/types"
	"reflect"
	"regexp"
	"strings"
)

const SliceRuleCode = "SLICE"

const (
	sliceOpRequiredCode = commonOpRequired
	sliceOpStopAllCode  = commonOpStopAll
	sliceOpMexCode      = commonOpMex
	sliceOpLenCode      = commonOpLen
	sliceOpElemCode     = "ELEM"
)

type sliceValidationOperation uint

const (
	SliceOpUnsupported = iota
	SliceOpRequired
	SliceOpStopAll
	SliceOpMex
	SliceOpLen
	SliceOpElem
)

func NewSliceValidator(field, defaultErrorCode string) *SliceValidator {
	bv := new(SliceValidator)
	bv.defaultErrorCode = defaultErrorCode
	bv.field = field
	bv.codesInUse = types.NewOrderedStringSet([]string{})
	bv.dependsFields = determinePathFields(field)
	bv.operations = make([]*sliceOperation, 0)
	bv.codesInUse.Add(bv.defaultErrorCode)
	bv.minLen = NoLimit
	bv.maxLen = NoLimit

	return bv
}

type SliceValidator struct {
	stopAll             bool
	codesInUse          types.StringSet
	dependsFields       types.StringSet
	defaultErrorCode    string
	field               string
	missingRequiredCode string
	required            bool
	operations          []*sliceOperation
	minLen              int
	maxLen              int
}

type sliceOperation struct {
	OpType        sliceValidationOperation
	ErrCode       string
	MExFields     types.StringSet
	elemValidator ValidationRule
}

func (bv *SliceValidator) IsSet(field string, subject interface{}) (bool, error) {

	ps, err := bv.extractReflectValue(field, subject)

	if err != nil {
		return false, err
	}

	if ps == nil {
		return false, nil
	}

	return true, nil
}

func (bv *SliceValidator) Validate(vc *ValidationContext) (result *ValidationResult, unexpected error) {

	f := bv.field

	if vc.OverrideField != "" {
		f = vc.OverrideField
	}

	sub := vc.Subject

	r := NewValidationResult()
	set, err := bv.IsSet(f, sub)

	if err != nil {
		return nil, err

	} else if !set {
		r.Unset = true

		if bv.required {
			r.AddForField(f, []string{bv.missingRequiredCode})
		}

		return r, nil
	}

	//Ignoring error as called previously during IsSet
	value, _ := bv.extractReflectValue(f, sub)

	err = bv.runOperations(f, value.(reflect.Value), vc, r)

	return r, err
}

func (sv *SliceValidator) runOperations(field string, v reflect.Value, vc *ValidationContext, r *ValidationResult) error {

	ec := types.NewEmptyOrderedStringSet()

	var err error

	for _, op := range sv.operations {

		switch op.OpType {
		case SliceOpMex:
			checkMExFields(op.MExFields, vc, ec, op.ErrCode)
		case SliceOpLen:
			if !sv.lengthOkay(v) {
				ec.Add(op.ErrCode)
			}
		case SliceOpElem:
			err = sv.checkElementContents(field, v, op.elemValidator, r, vc)
		}
	}

	r.AddForField(field, ec.Contents())

	return err

}

func (bv *SliceValidator) checkElementContents(field string, slice reflect.Value, v ValidationRule, r *ValidationResult, pvc *ValidationContext) error {

	stringElement := false
	nilable := false

	sl := slice.Len()

	var err error

	for i := 0; i < sl; i++ {

		fa := fmt.Sprintf("%s[%d]", field, i)

		vc := new(ValidationContext)
		vc.OverrideField = fa
		vc.KnownSetFields = pvc.KnownSetFields
		vc.DirectSubject = true

		e := slice.Index(i)

		switch tv := v.(type) {
		case *StringValidator:
			vc.Subject, err, nilable = bv.stringValue(e, fa)
			stringElement = true
		case *IntValidator:
			vc.Subject, err = tv.ToInt64(fa, e.Interface())
		case *FloatValidator:
			vc.Subject, err = tv.ToFloat64(fa, e.Interface())
		case *BoolValidationRule:
			vc.Subject, err = bv.boolValue(e, fa)
		}

		if err != nil {
			return err
		}

		vr, err := v.Validate(vc)

		if err != nil {
			return err
		}

		ee := vr.ErrorCodes[fa]

		r.AddForField(fa, ee)

		if stringElement {
			bv.overwriteStringValue(e, vc.Subject.(*types.NilableString), nilable)
		}

	}

	return nil
}

// String validation is unique in that it can modify the value under consideration
func (bv *SliceValidator) overwriteStringValue(v reflect.Value, ns *types.NilableString, wasNilable bool) {

	if !wasNilable {

		v.Set(reflect.ValueOf(ns.String()))
	}

}

func (bv *SliceValidator) stringValue(v reflect.Value, fa string) (*types.NilableString, error, bool) {

	s := v.Interface()

	switch s := s.(type) {
	case *types.NilableString:
		return s, nil, true
	case string:
		return types.NewNilableString(s), nil, false
	default:
		m := fmt.Sprintf("%s is not a string or *NilableString", fa)
		return nil, errors.New(m), false
	}

}

func (bv *SliceValidator) boolValue(v reflect.Value, fa string) (*types.NilableBool, error) {

	b := v.Interface()

	switch b := b.(type) {
	case *types.NilableBool:
		return b, nil
	case bool:
		return types.NewNilableBool(b), nil
	default:
		m := fmt.Sprintf("%s is not a bool or *NilableBool", fa)
		return nil, errors.New(m)
	}

}

func (bv *SliceValidator) extractReflectValue(f string, s interface{}) (interface{}, error) {

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

		return v, nil
	}

	m := fmt.Sprintf("%s is not a slice", f)

	return nil, errors.New(m)

}

func (sv *SliceValidator) Length(min, max int, code ...string) *SliceValidator {

	sv.minLen = min
	sv.maxLen = max

	ec := sv.chooseErrorCode(code)

	o := new(sliceOperation)
	o.OpType = SliceOpLen
	o.ErrCode = ec

	sv.addOperation(o)

	return sv

}

func (bv *SliceValidator) StopAllOnFail() bool {
	return bv.stopAll
}

func (bv *SliceValidator) CodesInUse() types.StringSet {
	return bv.codesInUse
}

func (bv *SliceValidator) DependsOnFields() types.StringSet {

	return bv.dependsFields
}

func (bv *SliceValidator) StopAll() *SliceValidator {

	bv.stopAll = true

	return bv
}

func (bv *SliceValidator) Required(code ...string) *SliceValidator {

	bv.required = true
	bv.missingRequiredCode = bv.chooseErrorCode(code)

	return bv
}

func (bv *SliceValidator) MEx(fields types.StringSet, code ...string) *SliceValidator {
	op := new(sliceOperation)
	op.ErrCode = bv.chooseErrorCode(code)
	op.OpType = SliceOpMex
	op.MExFields = fields

	bv.addOperation(op)

	return bv
}

func (bv *SliceValidator) Elem(v ValidationRule, code ...string) *SliceValidator {
	op := new(sliceOperation)
	op.ErrCode = bv.chooseErrorCode(code)
	op.OpType = SliceOpElem
	op.elemValidator = v

	bv.addOperation(op)

	return bv
}

func (bv *SliceValidator) addOperation(o *sliceOperation) {
	bv.operations = append(bv.operations, o)
}

func (bv *SliceValidator) chooseErrorCode(v []string) string {

	if len(v) > 0 {
		bv.codesInUse.Add(v[0])
		return v[0]
	} else {
		return bv.defaultErrorCode
	}

}

func (bv *SliceValidator) Operation(c string) (sliceValidationOperation, error) {
	switch c {
	case sliceOpRequiredCode:
		return SliceOpRequired, nil
	case sliceOpStopAllCode:
		return SliceOpStopAll, nil
	case sliceOpMexCode:
		return SliceOpMex, nil
	case sliceOpLenCode:
		return SliceOpLen, nil
	case sliceOpElemCode:
		return SliceOpElem, nil
	}

	m := fmt.Sprintf("Unsupported slice validation operation %s", c)
	return SliceOpUnsupported, errors.New(m)

}

func (sv *SliceValidator) lengthOkay(r reflect.Value) bool {

	if sv.minLen == NoLimit && sv.maxLen == NoLimit {
		return true
	}

	sl := r.Len()

	minOkay := sv.minLen == NoLimit || sl >= sv.minLen
	maxOkay := sv.maxLen == NoLimit || sl <= sv.maxLen

	return minOkay && maxOkay

}

func NewSliceValidatorBuilder(ec string, cf ioc.ComponentByNameFinder, rv *RuleValidator) *SliceValidatorBuilder {
	bv := new(SliceValidatorBuilder)
	bv.componentFinder = cf
	bv.defaultErrorCode = ec
	bv.sliceLenRegex = regexp.MustCompile(lengthPattern)
	bv.ruleValidator = rv

	return bv
}

type SliceValidatorBuilder struct {
	defaultErrorCode string
	componentFinder  ioc.ComponentByNameFinder
	sliceLenRegex    *regexp.Regexp
	ruleValidator    *RuleValidator
}

func (vb *SliceValidatorBuilder) parseRule(field string, rule []string) (ValidationRule, error) {

	defaultErrorcode := determineDefaultErrorCode(SliceRuleCode, rule, vb.defaultErrorCode)
	bv := NewSliceValidator(field, defaultErrorcode)

	for _, v := range rule {

		ops := decomposeOperation(v)
		opCode := ops[0]

		if isTypeIndicator(SliceRuleCode, opCode) {
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
		case SliceOpLen:
			err = vb.addLengthOperation(field, ops, bv)
		case SliceOpElem:
			err = vb.addElementValidationOperation(field, ops, v, bv)
		}

		if err != nil {

			return nil, err
		}

	}

	return bv, nil

}

func (vb *SliceValidatorBuilder) addElementValidationOperation(field string, ops []string, unparsedRule string, sv *SliceValidator) error {

	_, err := paramCount(ops, "Elem", field, 2, 3)

	if err != nil {
		return err
	}

	rv := vb.ruleValidator
	rule, err := rv.findRule(field, unparsedRule)

	if err != nil {
		return err
	}

	v, err := rv.parseRule(field, rule)

	if err != nil {
		return err
	}

	switch v.(type) {
	case *StringValidator, *BoolValidationRule, *IntValidator, *FloatValidator:
		break
	default:
		m := fmt.Sprintf("Only %s, %s, %s and %s rules may be used to validate slice elements. Field %s is trying to use %s",
			IntRuleCode, FloatRuleCode, boolRuleCode, StringRuleCode, field, rule[0])
		return errors.New(m)
	}

	sv.Elem(v, extractVargs(ops, 3)...)

	return nil
}

func (vb *SliceValidatorBuilder) addLengthOperation(field string, ops []string, sv *SliceValidator) error {

	_, err := paramCount(ops, "Length", field, 2, 3)

	if err != nil {
		return err
	}

	min, max, err := extractLengthParams(field, ops[1], vb.sliceLenRegex)

	if err != nil {
		return err
	}

	sv.Length(min, max, extractVargs(ops, 3)...)

	return nil

}

func (vb *SliceValidatorBuilder) captureExclusiveFields(field string, ops []string, bv *SliceValidator) error {
	_, err := paramCount(ops, "MEX", field, 2, 3)

	if err != nil {
		return err
	}

	members := strings.SplitN(ops[1], setMemberSep, -1)
	fields := types.NewOrderedStringSet(members)

	bv.MEx(fields, extractVargs(ops, 3)...)

	return nil

}

func (vb *SliceValidatorBuilder) markRequired(field string, ops []string, bv *SliceValidator) error {

	_, err := paramCount(ops, "Required", field, 1, 2)

	if err != nil {
		return err
	}

	bv.Required(extractVargs(ops, 2)...)

	return nil
}
