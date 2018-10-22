/* This source file is reference from https://github.com/ElementsProject/dicemix
*/
#include <vector>
#include <cstring>

#include "include/flint.h"
#include "include/fmpz.h"
#include "include/fmpz_mod_polyxx.h"

using namespace std;
using namespace flint;

#define DEBUG 0
#define STANDALONE 1

#define RET_INVALID           1
#define RET_INTERNAL_ERROR  100
#define RET_INPUT_ERROR     101

int solve_poly(vector<fmpzxx>& messages, const fmpzxx& p, const vector<fmpzxx>& sums) {
    vector<fmpzxx>::size_type n = sums.size();	 
    if (n < 2) {
#ifdef DEBUG
        cout << "Input vector too short." << endl;
#endif
        return RET_INPUT_ERROR;
    }

    // Basic sanity check to avoid weird inputs
    if (n > 1000) {
#ifdef DEBUG
        cout << "You probably do not want an input vector of more than 1000 elements. " << endl;
#endif
        return RET_INPUT_ERROR;
    }

    if (messages.size() != sums.size()) {
#ifdef DEBUG
        cout << "Output vector has wrong size." << endl;
#endif
        return RET_INPUT_ERROR;
    }

    if (p <= n) {
#ifdef DEBUG
        cout << "Prime must be larger than the size of the input vector." << endl;
#endif
        return RET_INPUT_ERROR;
    }

    fmpz_mod_polyxx poly(p);
    fmpz_mod_poly_factorxx factors;
    factors.fit_length(n);
    vector<fmpzxx> coeff(n);

    // Set lead coefficient
    poly.set_coeff(n, 1);

    fmpzxx inv;
    // Compute other coeffients
    for (vector<fmpzxx>::size_type i = 0; i < n; i++) {
        coeff[i] = sums[i];

        vector<fmpzxx>::size_type k = 0;
        // for j = i-1, ..., 0
        for (vector<fmpzxx>::size_type j = i; j-- > 0 ;) {
            coeff[i] += coeff[k] * sums[j];
            k++;
        }
        inv = i;
        inv = -(inv + 1u);
        inv = inv.invmod(p);
        coeff[i] *= inv;
        poly.set_coeff(n - i - 1, coeff[i]);
    }

#if defined(DEBUG) && defined(STANDALONE)
    //cout << "Polynomial: " << endl; print(poly); cout << endl << endl;
#endif

    // Factor
    factors.set_factor_kaltofen_shoup(poly);

#if defined(DEBUG) && defined(STANDALONE)
    //cout << "Factors: " << endl; print(factors); cout << endl << endl;
#endif

    vector<fmpzxx>::size_type n_roots = 0;
    for (int i = 0; i < factors.size(); i++) {
        if (factors.p(i).degree() != 1 || factors.p(i).lead() != 1) {
#if defined(DEBUG) && defined(STANDALONE)
            cout << "Non-monic factor." << endl;
#endif
            return RET_INVALID;
        }
        n_roots += factors.exp(i);
    }
    if (n_roots != n) {
#if defined(DEBUG) && defined(STANDALONE)
        cout << "Not enough roots." << endl;
#endif
        return RET_INVALID;
    }

    // Extract roots
    int k = 0;
    for (int i = 0; i < factors.size(); i++) {
        for (int j = 0; j < factors.exp(i); j++) {
            messages[k] = factors.p(i).get_coeff(0).negmod(p);			
            k++;			
        }
    }

    return 0;
}

//Solve polynomial with powersums and output message are base 16 
extern "C" int solve(char** out_messages, const char* prime, const char** const sums, size_t n, int base) {   
    try {
        fmpzxx p;
        fmpzxx m;

        vector<fmpzxx> s(n);
        vector<fmpzxx> messages(n);

        // operator= is hard-coded to base 10 and does not check for errors
        if (fmpz_set_str(p._fmpz(), prime, base)) {
            return RET_INPUT_ERROR;
        }

        for (size_t i = 0; i < n; i++) {
            if (fmpz_set_str(s[i]._fmpz(), sums[i], base)) {
                return RET_INPUT_ERROR;
            }
        }

        for (size_t i = 0; i < n; i++) {			
            if (out_messages[i] == NULL) {
				out_messages[i] = (char*)malloc(34 * sizeof(char));                
            }
        }

        int ret = solve_poly(messages, p, s);

        if (ret == 0) {
            for (size_t i = 0; i < n; i++) {
                // Impossible
                if (messages[i].sizeinbase(base) > strlen(prime)) {
                    return RET_INTERNAL_ERROR;
                }
                fmpz_get_str(out_messages[i], base, messages[i]._fmpz());
				
            }
        }
		
		
        return ret;
    } catch (...) {
        return RET_INTERNAL_ERROR;
    }
}


