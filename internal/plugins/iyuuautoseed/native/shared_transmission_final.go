package download

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"light-media/internal/plugins/downloadercommands"
)

// Change is the fixed optional port used only by the two source TR wrappers.
// It cannot select arbitrary RPC methods, keys, files, locations or credentials.
func (t *sharedTRCommands) Change(ctx context.Context, in downloadercommands.TRChangeCall) (any, error) {
	if t == nil || t.owner == nil {
		return nil, commandBoundary("missing fixed Transmission change client")
	}
	var direct *downloadercommands.SourceException
	var proof *mutationScope
	result, err := t.owner.run(ctx, func(c context.Context) (any, error) {
		proof = mutationScopeFor(c)
		args, err := sharedTRChangeArguments(in)
		if err != nil {
			return nil, err
		}
		if len(args) == 0 {
			// Pinned change_torrent checks args before _request/require_ids.
			direct = &downloadercommands.SourceException{Class: "ValueError", Message: "No arguments to set"}
			return nil, direct
		}
		// The existing constructor is lazy. Readonly session initialization
		// supplies the original cached RPC version without an extra request.
		if in.Labels != nil {
			if err = t.owner.trInit(c); err != nil {
				return nil, err
			}
			version, err := sharedTRChangeRPCVersion(t.owner.trSessionInfo)
			if err != nil {
				return nil, err
			}
			if version < 16 {
				if in.Log == nil {
					return nil, commandBoundary("missing trusted Transmission warning sink")
				}
				in.Log(c, "WARNING", fmt.Sprintf("Using feature not supported by server. RPC version for server %d, feature introduced in %d.", version, 16))
			}
		}
		selector, err := commandTRIDs(in.IDs)
		if err != nil {
			if source, ok := err.(*downloadercommands.SourceException); ok {
				direct = source
			}
			return nil, err
		}
		args["ids"] = selector
		if err = t.owner.trInit(c); err != nil {
			return nil, err
		}
		_, err = t.owner.trRequest(c, "torrent-set", args, true)
		return nil, err // Source ignores the nonthrowing return, not HTTP failure.
	})
	if rejected, ok := err.(*mutationNotDispatched); ok && direct != nil && rejected != nil &&
		rejected.seal == nativeMutationProofSeal && rejected.class == mutationFailureNative && rejected.cause == direct &&
		proof != nil && !proof.delegated.Load() && proof.terminal.Load() == mutationFailureNative && ctx != nil && ctx.Err() == nil {
		// Only this call's known local vendor validation, with unchanged exact
		// cause after final guard, may enter the leaf's source catch. No broad
		// errors.As unwrapping and no selected/owner/cancel downgrading.
		return result, direct
	}
	return result, err
}

func sharedTRChangeArguments(in downloadercommands.TRChangeCall) (map[string]any, error) {
	out := map[string]any{}
	if in.Labels != nil {
		out["labels"] = append([]string{}, (*in.Labels)...)
	}
	if in.UploadLimited != nil {
		out["uploadLimited"] = *in.UploadLimited
	}
	if in.UploadLimit != nil {
		out["uploadLimit"] = *in.UploadLimit
	}
	if in.DownloadLimited != nil {
		out["downloadLimited"] = *in.DownloadLimited
	}
	if in.DownloadLimit != nil {
		out["downloadLimit"] = *in.DownloadLimit
	}
	if in.SeedRatioMode != nil {
		out["seedRatioMode"] = *in.SeedRatioMode
	}
	if in.SeedIdleMode != nil {
		out["seedIdleMode"] = *in.SeedIdleMode
	}
	if in.SeedIdleLimit != nil {
		out["seedIdleLimit"] = *in.SeedIdleLimit
	}
	switch value := in.SeedRatioLimit.(type) {
	case nil:
	case int64:
		out["seedRatioLimit"] = value
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, commandBoundary("nonfinite prepared seed ratio")
		}
		// Python json retains 0.0/1.0 as float tokens; ordinary Go Marshal
		// would collapse them to integer tokens. The typed port preserves this.
		text := strconv.FormatFloat(value, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		out["seedRatioLimit"] = json.Number(text)
	default:
		return nil, commandBoundary("unprepared Transmission seed ratio")
	}
	return out, nil
}

func sharedTRChangeRPCVersion(session map[string]any) (int64, error) {
	value, present := session["rpc-version"]
	if !present {
		return 0, commandBoundary("Transmission session RPC version absent")
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, commandBoundary("Transmission session RPC version is not an integer")
	}
	version, err := number.Int64()
	if err != nil {
		return 0, commandBoundary("Transmission session RPC version outside finite integer domain")
	}
	return version, nil
}
