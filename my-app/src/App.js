import React, { useState, useEffect, useRef } from 'react';

function WishlistItems({displayedWishlistUser, wishlistUpToDate,
                        setWishlistUpToDate, loggedInUserInfo}) {
    const [data, setData] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);
    const [checkedItems, setCheckedItems] = useState([]);
        
    const updateCheckedItems = (itemId, isChecked) => {
        const updatedListOfItems = [...checkedItems];
        if (isChecked) {
            updatedListOfItems.push(itemId);
        } else {
            var idx = updatedListOfItems.indexOf(itemId)
            if (idx !== -1) {
                updatedListOfItems.splice(idx, 1)
            }
        }
        setCheckedItems(updatedListOfItems);
    }
    
    useEffect(() => {
        const fetchData = async () => {
            try {
                var url = '/api/wishlist?' + new URLSearchParams({
                        userId: displayedWishlistUser["id"]
                    }).toString()
                const response = await fetch(url, {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });

                if (!response.ok) {
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setData(await response.json());
                setWishlistUpToDate(true)
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchData();
    }, [displayedWishlistUser, wishlistUpToDate, setWishlistUpToDate]);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;

    // Extract column headers from the keys of the first object
    const columns = Object.keys(data["headers"]);

    const isOwner = displayedWishlistUser !== null && displayedWishlistUser["id"] === loggedInUserInfo["id"];

    async function doDelete() {
        try {
            // XXX: there may be stuff in checkedItems may be out of sync with what's actually
            // checked on screen
            
            const response = await fetch('/api/wishlist', {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({"ids": checkedItems })
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            setCheckedItems([])
            setWishlistUpToDate(false)
        } catch (error) {
            // XXX: handle this?
            console.log('Error deleting item: ' + error.message);
        }
    }
        
    return (
        <div>
                <table>
                    <thead>
                        <tr>
                            {!isOwner ? null : <th/> }
                            {columns.map((column, index) => (
                                <th key={index}>{column}</th>
                            ))}
                        </tr>
                    </thead>
                    <tbody>
                        {data["entries"] === null ? null : data["entries"].map((row, rowIndex) => (
                            <tr key={rowIndex}>
                                {!isOwner ? null :
                                <td>
                                     <input
                                         type="checkbox"
                                         checked={checkedItems.indexOf(row["id"]) !== -1}
                                         onChange={(event) =>
                                             updateCheckedItems(row["id"], event.target.checked)}
                                     />
                                </td>
                                }
                                {columns.map((column, colIndex) => (
                                     <td key={colIndex}>{row[column]}</td>
                                ))}
                            </tr>
                        ))}
                    </tbody>
                </table>
                {!isOwner ? null :
                 <>
                     <button onClick={doDelete} disabled={checkedItems.length === 0}>
                         Delete Selected Items
                     </button>
                     <br/>
                     <button onClick={() => setCheckedItems([])} disabled={checkedItems.length === 0}>
                         Clear Selection
                     </button>
                 </>
                }
                </div>
                );
}

function WishlistAdder({setWishlistUpToDate}) {
    const [formState, setFormState] = useState({
        description: '',
        source: '',
        cost: '',
        owner_notes: ''
    });
    const [postResponse, setPostResponse] = useState('');      

    const dialogRef = useRef(null);
    
    function handleDescription(event) {
        setFormState(formState => ({
            ...formState,
            description: event.target.value
        }))
    };

    function handleSource(event) {
        setFormState(formState => ({
            ...formState,
            source: event.target.value
        }))
    };

    function handleCost(event) {
        setFormState(formState => ({
            ...formState,
            cost: event.target.value
        }))
    };

    function handleOwnerNotes(event) {
        setFormState(formState => ({
            ...formState,
            owner_notes: event.target.value
        }))
    };

    async function doPost() {
        try {
            const response = await fetch('/api/wishlist', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formState)
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response.json()
            setFormState({
                description: '',
                source: '',
                cost: '',
                owner_notes: ''
            })
            setWishlistUpToDate(false)
            dialogRef.current.close();
        } catch (error) {
            setPostResponse('Error sending data: ' + error.message);
        }
    }
    
    return (
        <div>
            <button onClick={() => dialogRef.current.showModal()}>Add Item</button>

            <dialog ref={dialogRef}>
                            <h1> Add to Wishlist </h1>
            Description <br/>
            <input
                type="text"
                name="description"
                value={formState.description}
                onChange={handleDescription}
            /> <br/>
            Source <br/>
            <input
                type="text"
                name="source"
                value={formState.source}
                onChange={handleSource}
            /> <br/>
            Cost <br/>
            <input
                type="text"
                name="cost"
                value={formState.cost}
                onChange={handleCost}
            /> <br/>            
            Notes <br/>
            <input
                type="text"
                name="notes"
                value={formState.owner_notes}
                onChange={handleOwnerNotes}              
            /> <br/>
            <button onClick={doPost}>
                Add Item
            </button>
            {postResponse && <p>{postResponse}</p>}

      </dialog>            
        </div>
    );
}


function Login({setShowLogin, setLoggedInUserInfo}) {
    const [email, setEmail] = useState('');
    const [password, setPassword] = useState('');
    const [loginError, setLoginError] = useState('');

    function handleEmail(event) {
        setEmail(event.target.value);
    };

    function handlePassword(event) {
        setPassword(event.target.value);
    };

    async function doLogin() {
        try {
            const response = await fetch('/api/session', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    "email": email,
                    "password": password
                })
            });
            
            const data = await response.json()

            if (!response.ok) {
                setLoginError(data)
            } else {
                setLoggedInUserInfo(data)
            }
        } catch (error) {
            loginError(error.message)
            console.log('Error sending data: ' + error.message);
        }
    }

    return (
        <div>
            <h1> Login </h1>
            Email <br/>
            <input
                type="email"
                name="email"
                onChange={handleEmail}
            /> <br/>
            Password <br/>
            <input
                type="password"
                name="user_password"
                onChange={handlePassword}              
            /> <br/>
            <button onClick={doLogin}>
                Submit
            </button>
            {loginError && <p>{loginError}</p>}
            <p> don't have an account? </p>
            <button onClick={() => setShowLogin(false)}>
                Signup
            </button>            
        </div>
    );
}

function Signup({setShowLogin, setIsSignedIn}) {
    const [formState, setFormState] = useState({
        first: '',
        last: '',
        email: '',
        password: ''
    });
    const formRef = useRef(null);

    function handleFirstName(event) {
        setFormState(formState => ({
            ...formState,
            first: event.target.value
        }))
    };

    function handleLastName(event) {
        setFormState(formState => ({
            ...formState,
            last: event.target.value
        }))
    };

    function handleEmail(event) {
        setFormState(formState => ({
            ...formState,
            email: event.target.value
        }))
    };

    function handlePassword(event) {
        setFormState(formState => ({
            ...formState,
            password: event.target.value
        }))
    };

    async function handleSubmit(event) {
        event.preventDefault();
        if (!formRef.current.checkValidity()) {
            return
        }
        
        try {
            const response = await fetch('/api/signup', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(formState)
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response
            setIsSignedIn(true)
        } catch (error) {
            console.log('Error sending data: ' + error.message);
        }
    }

    return (
        <div>
            <h1> Signup </h1>
            First Name <br/>
            <form ref={formRef} onSubmit={handleSubmit}>
                <input
                    type="text"
                    name="firstname"
                    onChange={handleFirstName}
                /> <br/>
                Last Name <br/>
                <input
                    type="text"
                    name="lastname"
                    onChange={handleLastName}
                /> <br/>
                Email <br/>
                <input
                    type="email"
                    name="email"
                    required
                    onChange={handleEmail}
                /> <br/>            
                Password <br/>
                <input
                    type="password"
                    name="user_password"
                    onChange={handlePassword}              
                /> <br/>
                <input type="submit" value="Signup" />             
            </form>
            <p> Already have an account? </p>              
            <button onClick={() => setShowLogin(true)}>
                Login                                 
            </button>                                              
        </div>
    );
}

function LogoutButton({setLoggedInUserInfo}) {
    async function doLogout() {
        try {
            const response = await fetch('/api/session', {
                method: 'DELETE',
            });

            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            await response
            setLoggedInUserInfo(null)
        } catch (error) {
            console.log('error logging out' + error.message);
        }
    }

    return (
        <button onClick={doLogout}>
            Logout
        </button>
    );
}

function WishlistSelector({setDisplayedWishlistUser}) {
    const [data, setData] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);    
    
    useEffect(() => {
        const fetchData = async () => {
            try {
                const response = await fetch('/api/users', {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });
                if (!response.ok) {
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setData(await response.json());
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchData();
    }, []);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;

    return (
        <div>
            <h1>Choose Wishlist Owner</h1>
            <table>
                <tbody>
                    {data["users"] === null ? null : data["users"].map((row, rowIndex) => (
                        <tr key={rowIndex}>
                            <td onClick={() => setDisplayedWishlistUser({
                                    id: row["id"],
                                    first: row["first"],
                                    last: row["last"],
                                }
                                )}
                                style={{ cursor: 'pointer', color: 'blue' }}>
                                {row["first"] + " " + row["last"]}
                            </td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );    
}

function LoginOrSignup({setLoggedInUserInfo}) {
    const [showLogin, setShowLogin] = useState(true);
    return (
        <div>
            {showLogin ? <Login setShowLogin={setShowLogin} setLoggedInUserInfo={setLoggedInUserInfo}/>
             : <Signup setShowLogin={setShowLogin} setLoggedInUserInfo={setLoggedInUserInfo}/>}
        </div>
    );
}

function Wishlist({loggedInUserInfo}) {
    const [displayedWishlistUser, setDisplayedWishlistUser] = useState(loggedInUserInfo)
    const [wishlistUpToDate, setWishlistUpToDate] = useState(true)
    
    return (
        <div>
            <h1>{displayedWishlistUser["first"]}'s Wishlist</h1>
            {displayedWishlistUser !== null && displayedWishlistUser["id"] === loggedInUserInfo["id"] ? <WishlistAdder setWishlistUpToDate={setWishlistUpToDate}/> : null}
            <WishlistItems displayedWishlistUser={displayedWishlistUser}
                           wishlistUpToDate={wishlistUpToDate}
                           setWishlistUpToDate={setWishlistUpToDate}
                           loggedInUserInfo={loggedInUserInfo}/>
            <WishlistSelector setDisplayedWishlistUser={setDisplayedWishlistUser}/>
        </div>
    );
}

export default function MyApp() {

    const [loggedInUserInfo, setLoggedInUserInfo] = useState(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(null);        
    
    useEffect(() => {
        const fetchSession = async () => {
            try {
                // NB: this could probably be cached locally? Maybe the browswer can cache it too, idk
                const response = await fetch('/api/session', {
                    headers: {
                        'Content-Type': 'application/json',
                    },
                });

                if (!response.ok) {
                    if (response.status === 401) {
                        return
                    }
                    throw new Error(`HTTP error! status: ${response.status}`);
                }
                setLoggedInUserInfo(await response.json());
            } catch (error) {
                setError(error);
            } finally {
                setLoading(false);
            }
        };
        
        fetchSession();
    }, [setLoggedInUserInfo]);

    if (loading) return <p>Loading data...</p>;
    if (error) return <p>Error: {error.message}</p>;
    
    return (
        <div>
            {loggedInUserInfo !== null ?
             <div>
                 <Wishlist loggedInUserInfo={loggedInUserInfo}/>
                 <LogoutButton setLoggedInUserInfo={setLoggedInUserInfo}/>
             </div>
             : <LoginOrSignup setLoggedInUserInfo={setLoggedInUserInfo}/>}
        </div>
    );
}
