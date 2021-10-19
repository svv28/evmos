package ante

import (
	"github.com/palantir/stacktrace"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	channelkeeper "github.com/cosmos/ibc-go/modules/core/04-channel/keeper"
	ibcante "github.com/cosmos/ibc-go/modules/core/ante"

	"github.com/tharsis/ethermint/app/ante"
	evmtypes "github.com/tharsis/ethermint/x/evm/types"
)

const (
	secp256k1VerifyCost uint64 = 21000
)

// NewAnteHandler returns an ante handler responsible for attempting to route an
// Ethereum or SDK transaction to an internal ante handler for performing
// transaction-level processing (e.g. fee payment, signature verification) before
// being passed onto it's respective handler.
func NewAnteHandler(
	ak evmtypes.AccountKeeper,
	bankKeeper evmtypes.BankKeeper,
	evmKeeper ante.EVMKeeper,
	feeGrantKeeper authante.FeegrantKeeper,
	channelKeeper channelkeeper.Keeper,
	signModeHandler authsigning.SignModeHandler,
) sdk.AnteHandler {
	return func(
		ctx sdk.Context, tx sdk.Tx, sim bool,
	) (newCtx sdk.Context, err error) {
		var anteHandler sdk.AnteHandler

		defer ante.Recover(ctx.Logger(), &err)

		txWithExtensions, ok := tx.(authante.HasExtensionOptionsTx)
		if ok {
			opts := txWithExtensions.GetExtensionOptions()
			if len(opts) > 0 {
				switch typeURL := opts[0].GetTypeUrl(); typeURL {
				case "/ethermint.evm.v1.ExtensionOptionsEthereumTx":
					// handle as *evmtypes.MsgEthereumTx

					anteHandler = sdk.ChainAnteDecorators(
						ante.NewEthSetUpContextDecorator(), // outermost AnteDecorator. SetUpContext must be called first
						authante.NewMempoolFeeDecorator(),
						authante.NewTxTimeoutHeightDecorator(),
						authante.NewValidateMemoDecorator(ak),
						ante.NewEthValidateBasicDecorator(),
						ante.NewEthSigVerificationDecorator(evmKeeper),
						ante.NewEthAccountVerificationDecorator(ak, bankKeeper, evmKeeper),
						ante.NewEthNonceVerificationDecorator(ak),
						ante.NewEthGasConsumeDecorator(evmKeeper),
						ante.NewCanTransferDecorator(evmKeeper),
						ante.NewEthIncrementSenderSequenceDecorator(ak), // innermost AnteDecorator.
					)

				case "/ethermint.types.v1.ExtensionOptionsWeb3Tx":
					// handle as normal Cosmos SDK tx, except signature is checked for EIP712 representation

					switch tx.(type) {
					case sdk.Tx:
						anteHandler = sdk.ChainAnteDecorators(
							authante.NewSetUpContextDecorator(), // outermost AnteDecorator. SetUpContext must be called first
							authante.NewMempoolFeeDecorator(),
							authante.NewValidateBasicDecorator(),
							authante.NewTxTimeoutHeightDecorator(),
							authante.NewValidateMemoDecorator(ak),
							authante.NewConsumeGasForTxSizeDecorator(ak),
							authante.NewSetPubKeyDecorator(ak), // SetPubKeyDecorator must be called before all signature verification decorators
							authante.NewValidateSigCountDecorator(ak),
							authante.NewDeductFeeDecorator(ak, bankKeeper, feeGrantKeeper),
							authante.NewSigGasConsumeDecorator(ak, ante.DefaultSigVerificationGasConsumer),
							NewEip712SigVerificationDecorator(ak, signModeHandler), // overridden for EIP712 Tx signatures
							authante.NewIncrementSequenceDecorator(ak),             // innermost AnteDecorator
						)
					default:
						return ctx, sdkerrors.Wrapf(sdkerrors.ErrUnknownRequest, "invalid transaction type: %T", tx)
					}

				default:
					return ctx, stacktrace.Propagate(
						sdkerrors.Wrap(sdkerrors.ErrUnknownExtensionOptions, typeURL),
						"rejecting tx with unsupported extension option",
					)
				}

				return anteHandler(ctx, tx, sim)
			}
		}

		// handle as totally normal Cosmos SDK tx

		switch tx.(type) {
		case sdk.Tx:
			anteHandler = sdk.ChainAnteDecorators(
				authante.NewSetUpContextDecorator(), // outermost AnteDecorator. SetUpContext must be called first
				authante.NewRejectExtensionOptionsDecorator(),
				authante.NewMempoolFeeDecorator(),
				authante.NewValidateBasicDecorator(),
				authante.NewTxTimeoutHeightDecorator(),
				authante.NewValidateMemoDecorator(ak),
				ibcante.NewAnteDecorator(channelKeeper),
				authante.NewConsumeGasForTxSizeDecorator(ak),
				authante.NewSetPubKeyDecorator(ak), // SetPubKeyDecorator must be called before all signature verification decorators
				authante.NewValidateSigCountDecorator(ak),
				authante.NewDeductFeeDecorator(ak, bankKeeper, feeGrantKeeper),
				authante.NewSigGasConsumeDecorator(ak, ante.DefaultSigVerificationGasConsumer),
				authante.NewSigVerificationDecorator(ak, signModeHandler),
				authante.NewIncrementSequenceDecorator(ak), // innermost AnteDecorator
			)
		default:
			return ctx, stacktrace.Propagate(
				sdkerrors.Wrapf(sdkerrors.ErrUnknownRequest, "invalid transaction type: %T", tx),
				"transaction is not an SDK tx",
			)
		}

		return anteHandler(ctx, tx, sim)
	}
}
