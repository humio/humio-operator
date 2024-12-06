package humio

import (
	"fmt"
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func TestActionCRAsAction(t *testing.T) {
	type args struct {
		ha *humiov1alpha1.HumioAction
	}
	tests := []struct {
		name           string
		args           args
		wantErr        bool
		wantErrMessage string
	}{
		{
			"missing required emailProperties.recipients",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:            "action",
						EmailProperties: &humiov1alpha1.HumioActionEmailProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property emailProperties.recipients is required", ActionTypeEmail),
		},
		{
			"missing required humioRepository.ingestToken",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:                      "action",
						HumioRepositoryProperties: &humiov1alpha1.HumioActionRepositoryProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property humioRepositoryProperties.ingestToken is required", ActionTypeHumioRepo),
		},
		{
			"missing required opsGenieProperties.genieKey",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:               "action",
						OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property opsGenieProperties.genieKey is required, property opsGenieProperties.apiUrl is required", ActionTypeOpsGenie),
		},
		{
			"missing required pagerDutyProperties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:                "action",
						PagerDutyProperties: &humiov1alpha1.HumioActionPagerDutyProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property pagerDutyProperties.routingKey is required, property pagerDutyProperties.severity is required", ActionTypePagerDuty),
		},
		{
			"missing required slackProperties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:            "action",
						SlackProperties: &humiov1alpha1.HumioActionSlackProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property slackProperties.url is required, property slackProperties.fields is required, invalid url for slackProperties.url: parse \"\": empty url", ActionTypeSlack),
		},
		{
			"missing required slackPostMessageProperties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:                       "action",
						SlackPostMessageProperties: &humiov1alpha1.HumioActionSlackPostMessageProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property slackPostMessageProperties.apiToken is required, property slackPostMessageProperties.channels is required, property slackPostMessageProperties.fields is required", ActionTypeSlackPostMessage),
		},
		{
			"missing required victorOpsProperties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:                "action",
						VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property victorOpsProperties.notifyUrl is required, property victorOpsProperties.messageType is required, invalid url for victorOpsProperties.notifyUrl: parse \"\": empty url", ActionTypeVictorOps),
		},
		{
			"missing required webhookProperties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:              "action",
						WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: property webhookProperties.url is required, property webhookProperties.bodyTemplate is required, property webhookProperties.method is required, invalid url for webhookProperties.url: parse \"\": empty url", ActionTypeWebhook),
		},
		{
			"invalid pagerDutyProperties.severity",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name: "action",
						PagerDutyProperties: &humiov1alpha1.HumioActionPagerDutyProperties{
							RoutingKey: "routingkey",
							Severity:   "invalid",
						},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: unsupported severity for pagerDutyProperties: \"invalid\". must be one of: critical, error, warning, info", ActionTypePagerDuty),
		},
		{
			"invalid victorOpsProperties.messageType",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name: "action",
						VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{
							NotifyUrl:   "https://alert.victorops.com/integrations/0000/alert/0000/routing_key",
							MessageType: "invalid",
						},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: unsupported messageType for victorOpsProperties: \"invalid\". must be one of: critical, warning, acknowledgement, info, recovery", ActionTypeVictorOps),
		},
		{
			"invalid action multiple properties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name:                "action",
						VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{},
						EmailProperties:     &humiov1alpha1.HumioActionEmailProperties{},
					},
				},
			},
			true,
			fmt.Sprintf("could not find action type: found properties for more than one action: %s, %s", ActionTypeVictorOps, ActionTypeEmail),
		},
		{
			"invalid action missing properties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name: "action",
					},
				},
			},
			true,
			"could not find action type: no properties specified for action",
		},
		{
			"duplicate header in webhookProperties",
			args{
				&humiov1alpha1.HumioAction{
					Spec: humiov1alpha1.HumioActionSpec{
						Name: "action",
						WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
							Url:          "http://127.0.0.1",
							Method:       "POST",
							BodyTemplate: "some body",
							Headers: map[string]string{
								"key": "value",
							},
							SecretHeaders: []humiov1alpha1.HeadersSource{
								{
									Name:      "key",
									ValueFrom: humiov1alpha1.VarSource{},
								},
							},
						},
					},
				},
			},
			true,
			fmt.Sprintf("%s failed due to errors: webhookProperties contains duplicate keys", ActionTypeWebhook),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ActionFromActionCR(tt.args.ha)
			if (err != nil) != tt.wantErr {
				t.Errorf("ActionFromActionCR() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if err != nil && err.Error() != tt.wantErrMessage {
				t.Errorf("ActionFromActionCR() got = %v, want = %v", err.Error(), tt.wantErrMessage)
			}
		})
	}
}
