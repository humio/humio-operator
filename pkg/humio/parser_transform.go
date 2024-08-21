package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func ParserTransform(hp *humiov1alpha1.HumioParser) *humioapi.Parser {
	parser := &humioapi.Parser{
		Name:                           hp.Spec.Name,
		Script:                         hp.Spec.ParserScript,
		FieldsToTag:                    hp.Spec.TagFields,
		FieldsToBeRemovedBeforeParsing: []string{},
	}

	testCasesGQL := make([]humioapi.ParserTestCase, len(hp.Spec.TestData))
	for i := range hp.Spec.TestData {
		testCasesGQL[i] = humioapi.ParserTestCase{
			Event:      humioapi.ParserTestEvent{RawString: hp.Spec.TestData[i]},
			Assertions: []humioapi.ParserTestCaseAssertions{},
		}
	}
	parser.TestCases = testCasesGQL

	return parser
}
