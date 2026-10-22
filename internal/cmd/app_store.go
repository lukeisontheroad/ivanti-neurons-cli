package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/api"
	"github.com/spf13/cobra"
)

func newAppStoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Search Google Play and iTunes, and add store apps to the catalog",
	}
	cmd.AddCommand(newAppStoreSearchCmd(), newAppStoreViewCmd(), newAppStoreTracksCmd(), newAppStoreAddCmd())
	return cmd
}

func newAppStoreSearchCmd() *cobra.Command {
	var country string
	var platforms []string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <google|itunes> <term>",
		Short: "Search Google Play or the iTunes App Store for apps",
		Example: `  ivanti-neurons app store search google outlook
  ivanti-neurons app store search itunes outlook --country CH`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > api.MaxRows {
				return fmt.Errorf("--limit must be between 1 and %d", api.MaxRows)
			}
			q := params("q", args[1], "rows", strconv.Itoa(limit))
			var path string
			var cols []string
			switch args[0] {
			case "google":
				path, cols = "/api/v1/app/googleplay", []string{"name", "platformAppId", "cost", "category"}
			case "itunes":
				path, cols = "/api/v2/app/itunes", []string{"name", "bundleId", "appStoreId", "version", "developerName", "deviceFamily"}
				q.Set("countryCode", strings.ToUpper(country))
				for _, p := range platforms {
					q.Add("platformTypes", strings.ToUpper(p))
				}
			default:
				return fmt.Errorf("unknown store %q (want google or itunes)", args[0])
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			cm, err := contentSpaceID(cmd, client)
			if err != nil {
				return err
			}
			q.Set("cmPartitionId", cm)
			raw, err := client.Get(cmd.Context(), path, q)
			if err != nil {
				return err
			}
			return render(cmd, raw, cols)
		},
	}
	cmd.Flags().StringVar(&country, "country", "US", "iTunes store country code")
	cmd.Flags().StringSliceVar(&platforms, "platform", nil, "iTunes platform types, e.g. IOS or OSX (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of results (1-500)")
	return cmd
}

func newAppStoreViewCmd() *cobra.Command {
	var country string
	cmd := &cobra.Command{
		Use:   "view <google|itunes> <id>",
		Short: "Show a store app by package name (Google Play) or App Store id (iTunes)",
		Example: `  ivanti-neurons app store view google com.outlook.Z7
  ivanti-neurons app store view itunes 428974099`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			var q url.Values
			switch args[0] {
			case "google":
				if err := appCheckBundle(args[1]); err != nil {
					return err
				}
				path, q = "/api/v1/app/googleplay/"+args[1], url.Values{}
			case "itunes":
				if _, err := parseID(args[1], false); err != nil {
					return err
				}
				path, q = "/api/v1/app/itunes/"+args[1], params("countryCode", strings.ToUpper(country))
			default:
				return fmt.Errorf("unknown store %q (want google or itunes)", args[0])
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			cm, err := contentSpaceID(cmd, client)
			if err != nil {
				return err
			}
			q.Set("cmPartitionId", cm)
			raw, err := client.Get(cmd.Context(), path, q)
			if err != nil {
				return err
			}
			return appRenderApp(cmd, raw)
		},
	}
	cmd.Flags().StringVar(&country, "country", "US", "iTunes store country code (required by the API for itunes)")
	return cmd
}

func newAppStoreTracksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tracks <bundle-id>",
		Short: "List the Google Play app tracks (production, beta, ...) of an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			space, err := spaceID(cmd, client)
			if err != nil {
				return err
			}
			raw, err := client.Get(cmd.Context(), "/api/v1/app/appTracks", params("bundle-id", args[0], "dmPartitionId", space))
			if err != nil {
				return err
			}
			return render(cmd, raw, nil)
		},
	}
}

func newAppStoreAddCmd() *cobra.Command {
	var data string
	var fields []string
	cmd := &cobra.Command{
		Use:   "add <package-name>",
		Short: "Add a Google Play app to the catalog as an enterprise app",
		Long: `Add a public Google Play app (Android Enterprise) to the app catalog.
The JSON body holds the catalog settings: categoryIds, enableReviews,
enableRatings, distributionSettings {groupIds, accountIds, ruleGroupIds,
deviceIds}, configDistributionSettings (INSTALL, PROMOTION, ...).
dmPartitionId (current device space), platformType ANDROID, and appType
PUBLIC are filled in unless given. The result holds the new
applicationId and applicationVersionId.`,
		Example: `  ivanti-neurons app store add com.photography.goallearn -f categoryIds='[71482]' -f dmPartitionDistributionType=ALL
  ivanti-neurons app store add com.photography.goallearn --data @goallearn.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := appCheckBundle(args[0]); err != nil {
				return err
			}
			body, err := appBody(cmd, data, fields, map[string]any{"platformType": "ANDROID", "appType": "PUBLIC"})
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			if err := appSetSpace(cmd, client, body); err != nil {
				return err
			}
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/api/v2/app/public/google/"+args[0], nil, body)
			if err != nil {
				return err
			}
			return appReport(cmd, raw, "Added "+args[0])
		},
	}
	appDataFlags(cmd, &data, &fields)
	return cmd
}

func newAppReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "List, write, and delete Apps@Work app reviews",
	}
	var opts searchOpts
	list := &cobra.Command{
		Use:   "list",
		Short: "List app reviews in the content space",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			cm, err := contentSpaceID(cmd, client)
			if err != nil {
				return err
			}
			cols := []string{"id", "rating", "title", "appVersionName", "versionTag", "platformType", "displayName", "reviewState", "createdAt"}
			return runSearch(cmd, client, "/api/v1/app/review", &opts, params("cmPartitionId", cm), cols)
		},
	}
	opts.register(list)

	var rating int
	var title, summary string
	add := &cobra.Command{
		Use:   "add <version-id>",
		Short: "Rate and review an app version",
		Long: `Rate (1-5) and review an app version in Apps@Work, as the API user.
This call does not accept Basic auth: log in with an OAuth2 client
("auth login --client-id ...").`,
		Example: `  ivanti-neurons app review add 17026 --rating 5 --title "Must have!" --summary "I use it all the time."`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseID(args[0], false); err != nil {
				return err
			}
			if rating < 1 || rating > 5 {
				return fmt.Errorf("--rating must be between 1 and 5")
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			q := params("rating", strconv.Itoa(rating), "title", title, "summary", summary)
			raw, err := client.Call(cmd.Context(), http.MethodPost, "/c/api/v1/appstore/"+args[0]+"/review", q, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, "Reviewed app version "+args[0])
		},
	}
	add.Flags().IntVar(&rating, "rating", 0, "rating from 1 to 5 (required)")
	add.Flags().StringVar(&title, "title", "", "review title")
	add.Flags().StringVar(&summary, "summary", "", "review text")

	del := &cobra.Command{
		Use:     "delete <review-id>...",
		Short:   "Delete app reviews",
		Example: `  ivanti-neurons app review delete 49023 49024 --yes`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			client, err := newClient()
			if err != nil {
				return err
			}
			if err := confirm(cmd, fmt.Sprintf("delete %d review(s)", len(ids))); err != nil {
				return err
			}
			cm, err := contentSpaceID(cmd, client)
			if err != nil {
				return err
			}
			q := url.Values{"cmPartitionId": {cm}, "id": ids}
			raw, err := client.Call(cmd.Context(), http.MethodDelete, "/api/v1/app/review/delete", q, nil)
			if err != nil {
				return err
			}
			return report(cmd, raw, fmt.Sprintf("Deleted %d review(s)", len(ids)))
		},
	}
	cmd.AddCommand(list, add, del)
	return cmd
}
