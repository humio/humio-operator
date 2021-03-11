package humio

import (
	"fmt"
	"reflect"
	"testing"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func TestActionAsNotifier(t *testing.T) {
	type args struct {
		ha *humiov1alpha1.HumioAction
	}
	tests := []struct {
		name           string
		args           args
		want           *humioapi.Notifier
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property emailProperties.recipients is required", humioapi.NotifierTypeEmail),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property humioRepository.ingestToken is required", humioapi.NotifierTypeHumioRepo),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property opsGenieProperties.genieKey is required", humioapi.NotifierTypeOpsGenie),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property pagerDutyProperties.routingKey is required, property pagerDutyProperties.severity is required", humioapi.NotifierTypePagerDuty),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property slackProperties.fields is required, invalid url for slackProperties.url: parse \"\": empty url", humioapi.NotifierTypeSlack),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property slackPostMessageProperties.apiToken is required, property slackPostMessageProperties.channels is required, property slackPostMessageProperties.fields is required", humioapi.NotifierTypeSlackPostMessage),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property victorOpsProperties.messageType is required, property victorOpsProperties.notifyUrl is required", humioapi.NotifierTypeVictorOps),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: property webhookProperties.bodyTemplate is required, property webhookProperties.headers is required, property webhookProperties.method is required, property webhookProperties.url is required", humioapi.NotifierTypeWebHook),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: unsupported severity for PagerdutyProperties: \"invalid\". must be one of: critical, error, warning, info", humioapi.NotifierTypePagerDuty),
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
			&humioapi.Notifier{},
			true,
			fmt.Sprintf("%s failed due to errors: unsupported messageType for victorOpsProperties: \"invalid\". must be one of: critical, warning, acknowledgement, info, recovery", humioapi.NotifierTypeVictorOps),
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
			&humioapi.Notifier{},
			true,
			"could not find action type: found properties for more than one action: victorOpsProperties, emailProperties",
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
			&humioapi.Notifier{},
			true,
			"could not find action type: no properties specified for action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NotifierFromAction(tt.args.ha)
			if (err != nil) != tt.wantErr {
				t.Errorf("NotifierFromAction() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NotifierFromAction() got = %#v, want %#v", got, tt.want)
			}
			if err != nil && err.Error() != tt.wantErrMessage {
				t.Errorf("NotifierFromAction() got = %v, want %v", err.Error(), tt.wantErrMessage)
			}
		})
	}
}
