package ante

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/auth/types"

	ethermint "github.com/tharsis/ethermint/types"
	evmtypes "github.com/tharsis/ethermint/x/evm/types"
)

// DeductFeeDecorator deducts fees from the first signer of the tx
// If the first signer does not have the funds to pay for the fees, return with InsufficientFunds error
// Call next AnteHandler if fees successfully deducted
// CONTRACT: Tx must implement FeeTx interface to use DeductFeeDecorator
type DeductFeeDecorator struct {
	ak         evmtypes.AccountKeeper
	bankKeeper types.BankKeeper
}

func NewDeductFeeDecorator(ak evmtypes.AccountKeeper, bk types.BankKeeper) DeductFeeDecorator {
	return DeductFeeDecorator{
		ak:         ak,
		bankKeeper: bk,
	}
}

func (dfd DeductFeeDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return ctx, sdkerrors.Wrap(sdkerrors.ErrTxDecode, "Tx must be a FeeTx")
	}

	if addr := dfd.ak.GetModuleAddress(types.FeeCollectorName); addr == nil {
		panic(fmt.Sprintf("%s module account has not been set", types.FeeCollectorName))
	}

	var (
		feeDelegated bool
		feePayer     sdk.AccAddress
	)

	if txWithExtensions, ok := tx.(authante.HasExtensionOptionsTx); ok {
		if opts := txWithExtensions.GetExtensionOptions(); len(opts) > 0 {
			var optIface ethermint.ExtensionOptionsWeb3TxI

			if err := ethermintCodec.UnpackAny(opts[0], &optIface); err != nil {
				return ctx, sdkerrors.Wrap(sdkerrors.ErrUnpackAny, "failed to proto-unpack ExtensionOptionsWeb3Tx")
			}

			if extOpt, ok := optIface.(*ethermint.ExtensionOptionsWeb3Tx); ok {
				if len(extOpt.FeePayer) > 0 {
					feePayer, err = sdk.AccAddressFromBech32(extOpt.FeePayer)
					if err != nil {
						return ctx, sdkerrors.Wrapf(
							sdkerrors.ErrInvalidAddress,
							"failed to parse feePayer %s from ExtensionOptionsWeb3Tx", extOpt.FeePayer,
						)
					}

					feeDelegated = true
				}
			}
		}
	}

	if !feeDelegated {
		feePayer = feeTx.FeePayer()
	}

	feePayerAcc := dfd.ak.GetAccount(ctx, feePayer)
	if feePayerAcc == nil {
		return ctx, sdkerrors.Wrapf(sdkerrors.ErrUnknownAddress, "fee payer address: %s does not exist", feePayer)
	}

	// deduct the fees
	if !feeTx.GetFee().IsZero() {
		err := authante.DeductFees(dfd.bankKeeper, ctx, feePayerAcc, feeTx.GetFee())
		if err != nil {
			return ctx, err
		}
	}

	return next(ctx, tx, simulate)
}
